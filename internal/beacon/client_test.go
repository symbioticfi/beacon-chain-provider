package beacon

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestClient_GetGenesis(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/eth/v1/beacon/genesis", r.URL.Path)
		_, _ = fmt.Fprint(w, `{"data":{"genesis_time":"123"}}`)
	}))
	defer ts.Close()

	c := NewClient(ts.URL, time.Second)
	got, err := c.GetGenesis(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(123), got)
}

func TestClient_GetFinalizedSlot(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/eth/v1/beacon/headers/finalized", r.URL.Path)
		_, _ = fmt.Fprint(w, `{"data":{"header":{"message":{"slot":"777"}}}}`)
	}))
	defer ts.Close()

	c := NewClient(ts.URL, time.Second)
	got, err := c.GetFinalizedSlot(context.Background())
	require.NoError(t, err)
	require.Equal(t, uint64(777), got)
}

func TestClient_GetValidatorsByState(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/eth/v1/beacon/states/64/validators", r.URL.Path)
		statuses := r.URL.Query()["status"]
		sort.Strings(statuses)
		require.Equal(t, []string{"active_exiting", "active_ongoing"}, statuses)
		ids := r.URL.Query()["id"]
		sort.Strings(ids)
		require.Equal(t, []string{"0xabc", "0xdef"}, ids)
		_, _ = fmt.Fprint(w, `{"data":[{"validator":{"pubkey":"0xabc","effective_balance":"32000000000"}}]}`)
	}))
	defer ts.Close()

	c := NewClient(ts.URL, time.Second)
	got, err := c.GetValidatorsByState(context.Background(), "64", []string{"active_ongoing", "active_exiting"}, []string{"0xabc", "0xdef"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	require.Equal(t, "0xabc", got[0].Pubkey)
	require.Equal(t, uint64(32000000000), got[0].EffectiveBalance)
}

func TestClient_Non2xx(t *testing.T) {
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unavailable", http.StatusServiceUnavailable)
	}))
	defer ts.Close()

	c := NewClient(ts.URL, time.Second)
	_, err := c.GetGenesis(context.Background())
	require.Error(t, err)
	require.ErrorContains(t, err, "status=503")
}
