package marketapi

import (
	"context"
	"testing"

	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
)

// TestRegisterProviderRoundTripsModels covers the whole path a provider's model
// list takes over the RPC: in on RegisterProvider, normalized by the order book,
// and back out on both the register response and ListProviders.
func TestRegisterProviderRoundTripsModels(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()

	resp, err := h.client.RegisterProvider(ctx, &marketv1.RegisterProviderRequest{
		Id:           "gpu",
		Capacity:     10,
		PricePerUnit: 3,
		Models:       []string{"Llama-3.3-70B", "llama-3.3-70b", "qwen-2.5-72b"},
	})
	if err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}
	want := []string{"llama-3.3-70b", "qwen-2.5-72b"}
	if got := resp.GetProvider().GetModels(); len(got) != 2 || got[0] != want[0] || got[1] != want[1] {
		t.Fatalf("register response models = %v, want %v", got, want)
	}

	listed, err := h.client.ListProviders(ctx, &marketv1.ListProvidersRequest{})
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(listed.GetProviders()) != 1 {
		t.Fatalf("got %d providers, want 1", len(listed.GetProviders()))
	}
	if got := listed.GetProviders()[0].GetModels(); len(got) != 2 {
		t.Fatalf("listed models = %v, want %v", got, want)
	}
}

func TestListProvidersFiltersByModel(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()

	for _, p := range []*marketv1.RegisterProviderRequest{
		{Id: "expensive", Capacity: 10, PricePerUnit: 9, Models: []string{"llama-3.3-70b"}},
		{Id: "cheap", Capacity: 10, PricePerUnit: 2, Models: []string{"llama-3.3-70b"}},
		{Id: "other", Capacity: 10, PricePerUnit: 1, Models: []string{"qwen-2.5-72b"}},
		{Id: "compute-only", Capacity: 10, PricePerUnit: 1},
	} {
		if _, err := h.client.RegisterProvider(ctx, p); err != nil {
			t.Fatalf("RegisterProvider %s: %v", p.GetId(), err)
		}
	}

	// The filter is matched case-insensitively and ordered cheapest first, so a
	// gateway can take the head of the list.
	resp, err := h.client.ListProviders(ctx, &marketv1.ListProvidersRequest{Model: "LLAMA-3.3-70B"})
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	got := resp.GetProviders()
	if len(got) != 2 {
		t.Fatalf("got %d providers, want 2 (compute-only and the other model excluded): %v", len(got), got)
	}
	if got[0].GetId() != "cheap" || got[1].GetId() != "expensive" {
		t.Fatalf("order = %s, %s; want cheap, expensive", got[0].GetId(), got[1].GetId())
	}
}

func TestListProvidersWithNoModelFilterIsUnchanged(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()

	if _, err := h.client.RegisterProvider(ctx, &marketv1.RegisterProviderRequest{
		Id: "compute-only", Capacity: 10, PricePerUnit: 1,
	}); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}

	// A provider that advertises no model is invisible to a model query but must
	// stay visible to the plain listing, which is the compute marketplace's view.
	resp, err := h.client.ListProviders(ctx, &marketv1.ListProvidersRequest{})
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	if len(resp.GetProviders()) != 1 {
		t.Fatalf("got %d providers, want 1", len(resp.GetProviders()))
	}
}

func TestListProvidersByModelSkipsAProviderWithNoCapacityLeft(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()

	for _, p := range []*marketv1.RegisterProviderRequest{
		{Id: "full", Capacity: 1, PricePerUnit: 1, Models: []string{"m"}},
		{Id: "free", Capacity: 5, PricePerUnit: 4, Models: []string{"m"}},
	} {
		if _, err := h.client.RegisterProvider(ctx, p); err != nil {
			t.Fatalf("RegisterProvider %s: %v", p.GetId(), err)
		}
	}
	if err := h.market.Ledger().Credit("buyer", 1_000); err != nil {
		t.Fatalf("credit: %v", err)
	}
	if _, err := h.client.SubmitJob(ctx, &marketv1.SubmitJobRequest{
		Buyer: "buyer", Provider: "full", Units: 1,
	}); err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}

	// "Who can serve this model" has to mean "and has room", or a router sends
	// the cheapest provider a job it will refuse.
	resp, err := h.client.ListProviders(ctx, &marketv1.ListProvidersRequest{Model: "m"})
	if err != nil {
		t.Fatalf("ListProviders: %v", err)
	}
	got := resp.GetProviders()
	if len(got) != 1 || got[0].GetId() != "free" {
		t.Fatalf("got %v, want only the provider with capacity left", got)
	}
}
