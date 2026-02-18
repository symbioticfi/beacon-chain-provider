package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/ethereum/go-ethereum/common"
	"github.com/go-viper/mapstructure/v2"
	"github.com/spf13/cobra"
	"github.com/spf13/viper"
	votingpowerv1 "github.com/symbioticfi/beacon-chain-provider/api/proto/votingpower/v1"
	"github.com/symbioticfi/beacon-chain-provider/internal/beacon"
	"github.com/symbioticfi/beacon-chain-provider/internal/keyregistry"
	"github.com/symbioticfi/beacon-chain-provider/internal/provider"
	"github.com/symbioticfi/beacon-chain-provider/internal/server"
	symbiotic "github.com/symbioticfi/relay/symbiotic/entity"
	"google.golang.org/grpc"
)

const (
	defaultConfigPath     = "config.yaml"
	defaultGRPCListen     = ":9090"
	defaultRequestTimeout = 5 * time.Second
	defaultLogLevel       = "info"
)

type AppConfig struct {
	GRPC struct {
		Listen string `mapstructure:"listen"`
	} `mapstructure:"grpc"`
	Beacon struct {
		NodeURL string `mapstructure:"node_url"`
	} `mapstructure:"beacon"`
	Ethereum struct {
		RPCURL string `mapstructure:"rpc_url"`
	} `mapstructure:"ethereum"`
	KeyRegistry struct {
		Address string `mapstructure:"address"`
		ChainID uint64 `mapstructure:"chain_id"`
		KeyTag  uint8  `mapstructure:"key_tag"`
	} `mapstructure:"key_registry"`
	Timeouts struct {
		Request time.Duration `mapstructure:"request"`
	} `mapstructure:"timeouts"`
	Log struct {
		Level string `mapstructure:"level"`
	} `mapstructure:"log"`
	Provider struct {
		Mock bool `mapstructure:"mock"`
	} `mapstructure:"provider"`
}

func (c AppConfig) Validate() error {
	if c.Beacon.NodeURL == "" {
		return errors.New("beacon.node_url is required")
	}
	if c.Ethereum.RPCURL == "" {
		return errors.New("ethereum.rpc_url is required")
	}
	if !common.IsHexAddress(c.KeyRegistry.Address) {
		return errors.New("key_registry.address must be a valid hex address")
	}
	if c.KeyRegistry.ChainID == 0 {
		return errors.New("key_registry.chain_id is required")
	}
	if !c.Provider.Mock && symbiotic.KeyTag(c.KeyRegistry.KeyTag).Type() != symbiotic.KeyTypeBls12381 {
		return errors.New("key_registry.key_tag must be BLS12-381 type")
	}
	return nil
}

func main() {
	if err := NewRootCommand().Execute(); err != nil && !errors.Is(err, context.Canceled) {
		slog.Error("Error executing command", "error", err)
		os.Exit(1)
	}
	slog.Info("Beacon voting power provider completed successfully")
}

func NewRootCommand() *cobra.Command {
	var cmdCfg AppConfig

	cmd := &cobra.Command{
		Use:           "beacon-vp",
		Short:         "Beacon-chain voting power provider",
		Long:          "A gRPC service that provides voting power derived from beacon validators and key registry ownership.",
		SilenceUsage:  true,
		SilenceErrors: true,
		PersistentPreRunE: func(cmd *cobra.Command, args []string) error {
			return initConfig(cmd, &cmdCfg)
		},
		RunE: func(cmd *cobra.Command, args []string) error {
			return runApp(signalContext(cmd.Context()), cmdCfg)
		},
	}

	addRootFlags(cmd)
	return cmd
}

func addRootFlags(cmd *cobra.Command) {
	cmd.PersistentFlags().String("config", defaultConfigPath, "Path to YAML config file")
	cmd.PersistentFlags().String("grpc.listen", defaultGRPCListen, "gRPC listen address")
	cmd.PersistentFlags().String("beacon.node_url", "", "Beacon node URL")
	cmd.PersistentFlags().String("ethereum.rpc_url", "", "Ethereum RPC URL")
	cmd.PersistentFlags().String("key_registry.address", "", "Key registry contract address")
	cmd.PersistentFlags().Uint64("key_registry.chain_id", 0, "Key registry chain ID")
	cmd.PersistentFlags().Uint8("key_registry.key_tag", 0, "Key tag filter")
	cmd.PersistentFlags().Duration("timeouts.request", defaultRequestTimeout, "Request timeout")
	cmd.PersistentFlags().String("log.level", defaultLogLevel, "Log level")
	cmd.PersistentFlags().Bool("provider.mock", false, "Enable deterministic mock mapping from operators to hoodi beacon pubkeys")
}

func initConfig(cmd *cobra.Command, cfg *AppConfig) error {
	v := viper.New()
	v.SetEnvPrefix("BEACON_VP")
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_", "-", "_"))
	v.AutomaticEnv()
	v.SetDefault("grpc.listen", defaultGRPCListen)
	v.SetDefault("timeouts.request", defaultRequestTimeout)
	v.SetDefault("log.level", defaultLogLevel)

	if err := v.BindPFlags(cmd.Flags()); err != nil {
		return fmt.Errorf("bind flags: %w", err)
	}

	cfgPath, err := cmd.Flags().GetString("config")
	if err != nil {
		return fmt.Errorf("get config flag: %w", err)
	}
	v.SetConfigFile(cfgPath)

	if err := v.ReadInConfig(); err != nil {
		return fmt.Errorf("read config: %w", err)
	}

	if err := v.Unmarshal(cfg, func(dc *mapstructure.DecoderConfig) {
		dc.DecodeHook = mapstructure.ComposeDecodeHookFunc(
			mapstructure.StringToTimeDurationHookFunc(),
		)
	}); err != nil {
		return fmt.Errorf("unmarshal config: %w", err)
	}

	return cfg.Validate()
}

func runApp(ctx context.Context, cfg AppConfig) error {
	logger := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: parseLogLevel(cfg.Log.Level)}))

	beaconClient := beacon.NewClient(cfg.Beacon.NodeURL, cfg.Timeouts.Request)
	keyRegistryClient, err := keyregistry.NewClient(ctx,
		cfg.Ethereum.RPCURL,
		common.HexToAddress(cfg.KeyRegistry.Address),
		cfg.KeyRegistry.KeyTag,
		cfg.KeyRegistry.ChainID,
	)
	if err != nil {
		return err
	}

	options := make([]provider.Option, 0, 1)
	if cfg.Provider.Mock {
		options = append(options, provider.WithMockMap(""))
	}
	options = append(options, provider.WithLogger(logger))
	votingProvider := provider.New(beaconClient, keyRegistryClient, options...)
	grpcService := server.NewGRPCServer(logger, votingProvider)

	lis, err := net.Listen("tcp", cfg.GRPC.Listen)
	if err != nil {
		return err
	}

	grpcServer := grpc.NewServer(
		grpc.UnaryInterceptor(timeoutUnaryInterceptor(cfg.Timeouts.Request)),
	)
	votingpowerv1.RegisterVotingPowerProviderServiceServer(grpcServer, grpcService)

	errCh := make(chan error, 1)
	go func() {
		logger.Info("starting grpc server", "listen", cfg.GRPC.Listen)
		errCh <- grpcServer.Serve(lis)
	}()

	select {
	case <-ctx.Done():
		logger.Info("shutdown signal received")
	case err := <-errCh:
		if err != nil {
			return err
		}
	}

	stopped := make(chan struct{})
	go func() {
		grpcServer.GracefulStop()
		close(stopped)
	}()

	select {
	case <-stopped:
		logger.Info("grpc server stopped")
	case <-time.After(10 * time.Second):
		logger.Warn("graceful stop timeout; forcing stop")
		grpcServer.Stop()
	}

	return nil
}

func signalContext(ctx context.Context) context.Context {
	cnCtx, cancel := context.WithCancel(ctx)

	c := make(chan os.Signal, 1)
	signal.Notify(c, syscall.SIGTERM, syscall.SIGINT)

	go func() {
		sig := <-c
		slog.Info("Received signal", "signal", sig)
		cancel()
	}()

	return cnCtx
}

func timeoutUnaryInterceptor(timeout time.Duration) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req interface{}, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (interface{}, error) {
		ctx, cancel := context.WithTimeout(ctx, timeout)
		defer cancel()
		return handler(ctx, req)
	}
}

func parseLogLevel(level string) slog.Leveler {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case "debug":
		return slog.LevelDebug
	case "warn":
		return slog.LevelWarn
	case "error":
		return slog.LevelError
	default:
		return slog.LevelInfo
	}
}
