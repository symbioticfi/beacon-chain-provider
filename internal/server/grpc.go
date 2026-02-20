package server

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"

	votingpowerv1 "github.com/symbioticfi/beacon-chain-provider/api/proto/votingpower/v1"
	"github.com/symbioticfi/beacon-chain-provider/internal/provider"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

type GRPCServer struct {
	votingpowerv1.UnimplementedVotingPowerProviderServiceServer
	logger   *slog.Logger
	provider *provider.Provider
}

func NewGRPCServer(logger *slog.Logger, provider *provider.Provider) *GRPCServer {
	return &GRPCServer{logger: logger, provider: provider}
}

func (s *GRPCServer) GetVotingPowersAt(ctx context.Context, req *votingpowerv1.GetVotingPowersAtRequest) (*votingpowerv1.GetVotingPowersAtResponse, error) {
	if req == nil {
		return nil, status.Error(codes.InvalidArgument, "request is required")
	}

	result, meta, err := s.provider.GetVotingPowersAt(ctx, req.GetTimestamp())
	if err != nil {
		code, msg := mapProviderError(err)
		s.logger.WarnContext(ctx, "GetVotingPowersAt failed", "timestamp", req.GetTimestamp(), "code", code.String(), "error", err)
		return nil, status.Error(code, msg)
	}

	resp := &votingpowerv1.GetVotingPowersAtResponse{
		VotingPowers: make([]*votingpowerv1.OperatorVotingPower, 0, len(result)),
	}
	for _, row := range result {
		resp.VotingPowers = append(resp.VotingPowers, &votingpowerv1.OperatorVotingPower{
			Operator:    row.Operator.Hex(),
			VotingPower: strconv.FormatUint(row.VotingPowerGwei, 10),
		})
	}

	s.logger.InfoContext(ctx, "GetVotingPowersAt ok",
		"timestamp", req.GetTimestamp(),
		"epoch", meta.Epoch,
		"slot", meta.Slot,
		"matched_validators", meta.MatchedValidator,
		"operator_count", meta.OperatorCount,
	)
	return resp, nil
}

func mapProviderError(err error) (codes.Code, string) {
	if errors.Is(err, provider.ErrMalformedRequest) || errors.Is(err, provider.ErrTimestampBeforeGenesis) {
		return codes.InvalidArgument, err.Error()
	}
	if errors.Is(err, provider.ErrEpochNotFinalized) || errors.Is(err, provider.ErrDuplicatePubkeyOwnership) {
		return codes.FailedPrecondition, err.Error()
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return codes.DeadlineExceeded, err.Error()
	}
	if errors.Is(err, context.Canceled) {
		return codes.Canceled, err.Error()
	}
	if errors.Is(err, provider.ErrUpstream) {
		return codes.Unavailable, err.Error()
	}
	return codes.Internal, fmt.Sprintf("internal error: %v", err)
}
