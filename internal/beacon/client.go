package beacon

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/symbioticfi/beacon-chain-provider/internal/types"
)

const (
	pathGenesis          = "/eth/v1/beacon/genesis"
	pathFinalizedHeader  = "/eth/v1/beacon/headers/finalized"
	pathStateValidators  = "/eth/v1/beacon/states/%s/validators"
	maxErrorBodyReadSize = 2048
)

type httpStatusError struct {
	StatusCode int
	Body       string
}

func (e *httpStatusError) Error() string {
	return fmt.Sprintf("beacon http status=%d body=%s", e.StatusCode, e.Body)
}

func (e *httpStatusError) NotFound() bool {
	return e.StatusCode == http.StatusNotFound
}

func IsNotFoundError(err error) bool {
	if err == nil {
		return false
	}
	var statusErr *httpStatusError
	return errors.As(err, &statusErr) && statusErr.NotFound()
}

type genesisResponse struct {
	Data struct {
		GenesisTime string `json:"genesis_time"`
	} `json:"data"`
}

type finalizedHeaderResponse struct {
	Data struct {
		Header struct {
			Message struct {
				Slot string `json:"slot"`
			} `json:"message"`
		} `json:"header"`
	} `json:"data"`
}

type validatorData struct {
	Validator struct {
		Pubkey           string `json:"pubkey"`
		EffectiveBalance string `json:"effective_balance"`
	} `json:"validator"`
}

type validatorsResponse struct {
	Data []validatorData `json:"data"`
}

type Client struct {
	baseURL string
	http    *http.Client
}

func NewClient(baseURL string, timeout time.Duration) *Client {
	return &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		http: &http.Client{
			Timeout: timeout,
		},
	}
}

func (c *Client) GetGenesis(ctx context.Context) (uint64, error) {
	var resp genesisResponse
	if err := c.get(ctx, pathGenesis, nil, &resp); err != nil {
		return 0, err
	}
	v, err := strconv.ParseUint(resp.Data.GenesisTime, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse genesis_time: %w", err)
	}
	return v, nil
}

func (c *Client) GetFinalizedSlot(ctx context.Context) (uint64, error) {
	var resp finalizedHeaderResponse
	if err := c.get(ctx, pathFinalizedHeader, nil, &resp); err != nil {
		return 0, err
	}
	v, err := strconv.ParseUint(resp.Data.Header.Message.Slot, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("parse finalized slot: %w", err)
	}
	return v, nil
}

func (c *Client) GetValidatorsByState(ctx context.Context, stateID string, statuses []string, ids []string) ([]types.BeaconValidator, error) {
	query := make(url.Values)
	for _, s := range statuses {
		query.Add("status", s)
	}
	for _, id := range ids {
		query.Add("id", id)
	}
	var resp validatorsResponse
	if err := c.get(ctx, fmt.Sprintf(pathStateValidators, stateID), query, &resp); err != nil {
		return nil, err
	}

	out := make([]types.BeaconValidator, 0, len(resp.Data))
	for _, row := range resp.Data {
		bal, err := strconv.ParseUint(row.Validator.EffectiveBalance, 10, 64)
		if err != nil {
			return nil, fmt.Errorf("parse effective_balance: %w", err)
		}
		out = append(out, types.BeaconValidator{
			Pubkey:           row.Validator.Pubkey,
			EffectiveBalance: bal,
		})
	}
	return out, nil
}

func (c *Client) get(ctx context.Context, path string, query url.Values, out interface{}) error {
	u, err := url.Parse(c.baseURL + path)
	if err != nil {
		return fmt.Errorf("parse url: %w", err)
	}
	u.RawQuery = query.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return fmt.Errorf("new request: %w", err)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("http do: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyReadSize))
		return &httpStatusError{StatusCode: resp.StatusCode, Body: strings.TrimSpace(string(b))}
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode response: %w", err)
	}
	return nil
}
