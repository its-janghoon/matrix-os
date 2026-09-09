package marketapi

import (
	"context"
	"math"
	"strings"
	"testing"
	"time"

	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"

	"github.com/ecirlabs/matrix-core/internal/market"
	"github.com/ecirlabs/matrix-core/internal/marketexchange"
)

func TestRegisterProviderQuoteRoundTripAndGeneratedDefaults(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	observed := time.Now().UTC().Add(-time.Minute).Truncate(time.Second)
	valid := observed.Add(2 * time.Hour)

	resp, err := h.client.RegisterProvider(ctx, &marketv1.RegisterProviderRequest{
		Id: "gpu", Capacity: 10, PricePerUnit: 12,
		CostPerUnit: proto.Uint64(8), MarkupBasisPoints: proto.Uint32(250),
		QuoteId: "quote-api", QuoteVersion: 7,
		ObservedAt: timestamppb.New(observed), ValidUntil: timestamppb.New(valid),
		Models: []string{"Model-B"},
	})
	if err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}
	got := resp.GetProvider()
	if got.GetPricePerUnit() != 12 || got.GetCostPerUnit() != 8 || got.GetMarkupBasisPoints() != 250 {
		t.Fatalf("price metadata did not round trip: %+v", got)
	}
	if got.GetQuoteId() != "quote-api" || got.GetQuoteVersion() != 7 {
		t.Fatalf("quote identity did not round trip: %+v", got)
	}
	if !got.GetObservedAt().AsTime().Equal(observed) || !got.GetValidUntil().AsTime().Equal(valid) {
		t.Fatalf("quote times did not round trip: %s / %s", got.GetObservedAt(), got.GetValidUntil())
	}

	defaults, err := h.client.RegisterProvider(ctx, &marketv1.RegisterProviderRequest{
		Id: "defaults", Capacity: 1, PricePerUnit: 1,
	})
	if err != nil {
		t.Fatalf("RegisterProvider(defaults): %v", err)
	}
	if defaults.GetProvider().GetQuoteId() == "" || defaults.GetProvider().GetQuoteVersion() != 1 ||
		defaults.GetProvider().GetObservedAt() == nil || defaults.GetProvider().GetValidUntil() == nil {
		t.Fatalf("market defaults missing from API response: %+v", defaults.GetProvider())
	}
}

func TestUpdateProviderQuoteRPCPreservesProviderShapeAndJobSnapshot(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()
	if _, err := h.client.RegisterProvider(ctx, &marketv1.RegisterProviderRequest{
		Id: "gpu", Capacity: 10, PricePerUnit: 3, QuoteId: "quote-1", QuoteVersion: 2,
		Models: []string{"Model-A"},
	}); err != nil {
		t.Fatalf("RegisterProvider: %v", err)
	}
	if err := h.market.Ledger().Credit("buyer", 100); err != nil {
		t.Fatalf("Credit: %v", err)
	}
	jobResp, err := h.client.SubmitJob(ctx, &marketv1.SubmitJobRequest{Buyer: "buyer", Provider: "gpu", Units: 4})
	if err != nil {
		t.Fatalf("SubmitJob: %v", err)
	}

	updated, err := h.client.UpdateProviderQuote(ctx, &marketv1.UpdateProviderQuoteRequest{
		Id: "gpu", PricePerUnit: 9, CostPerUnit: proto.Uint64(6), MarkupBasisPoints: proto.Uint32(125),
		QuoteId: "quote-2", QuoteVersion: 3,
	})
	if err != nil {
		t.Fatalf("UpdateProviderQuote: %v", err)
	}
	if updated.GetProvider().GetCapacity() != 10 || updated.GetProvider().GetAvailable() != 6 {
		t.Fatalf("capacity/reservation changed: %+v", updated.GetProvider())
	}
	if got := updated.GetProvider().GetModels(); len(got) != 1 || got[0] != "model-a" {
		t.Fatalf("models changed: %v", got)
	}
	if updated.GetProvider().GetPricePerUnit() != 9 || updated.GetProvider().GetQuoteId() != "quote-2" {
		t.Fatalf("quote not updated: %+v", updated.GetProvider())
	}

	job := jobResp.GetJob()
	if job.GetPricePerUnit() != 3 || job.GetPrice() != 12 || job.GetQuoteId() != "quote-1" || job.GetQuoteVersion() != 2 {
		t.Fatalf("job API snapshot = %+v, want original quote", job)
	}
	if job.GetQuoteObservedAt() == nil || job.GetQuoteValidUntil() == nil {
		t.Fatalf("job API omitted quote timestamps: %+v", job)
	}
}

func TestProviderQuoteRPCRejectsInvalidQuotesAndOverflow(t *testing.T) {
	h := newTestHarness(t)
	ctx := context.Background()

	_, err := h.client.RegisterProvider(ctx, &marketv1.RegisterProviderRequest{
		Id: "cost-only", Capacity: 1, CostPerUnit: proto.Uint64(3),
	})
	if status.Code(err) != codes.InvalidArgument || !strings.Contains(err.Error(), "final price") {
		t.Fatalf("cost-only registration error = %v, want InvalidArgument explaining final price", err)
	}

	now := time.Now().UTC()
	_, err = h.client.RegisterProvider(ctx, &marketv1.RegisterProviderRequest{
		Id: "stale", Capacity: 1, PricePerUnit: 1,
		ObservedAt: timestamppb.New(now.Add(-2 * time.Hour)), ValidUntil: timestamppb.New(now.Add(-time.Hour)),
	})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("stale registration code = %v (%v), want FailedPrecondition", status.Code(err), err)
	}
	_, err = h.client.RegisterProvider(ctx, &marketv1.RegisterProviderRequest{
		Id: "future", Capacity: 1, PricePerUnit: 1,
		ObservedAt: timestamppb.New(now.Add(market.MaxQuoteClockSkew + time.Minute)), ValidUntil: timestamppb.New(now.Add(2 * time.Hour)),
	})
	if status.Code(err) != codes.InvalidArgument {
		t.Fatalf("future registration code = %v (%v), want InvalidArgument", status.Code(err), err)
	}

	if _, err := h.client.RegisterProvider(ctx, &marketv1.RegisterProviderRequest{
		Id: "overflow", Capacity: 2, PricePerUnit: math.MaxUint64,
	}); err != nil {
		t.Fatalf("RegisterProvider(overflow): %v", err)
	}
	_, err = h.client.SubmitJob(ctx, &marketv1.SubmitJobRequest{Buyer: "buyer", Provider: "overflow", Units: 2})
	if status.Code(err) != codes.FailedPrecondition {
		t.Fatalf("overflow submit code = %v (%v), want FailedPrecondition", status.Code(err), err)
	}

	_, err = h.client.UpdateProviderQuote(ctx, &marketv1.UpdateProviderQuoteRequest{Id: "missing", PricePerUnit: 1})
	if status.Code(err) != codes.NotFound {
		t.Fatalf("missing update code = %v (%v), want NotFound", status.Code(err), err)
	}
}

func TestRemoteProviderQuoteMappingUsesEmbeddedQuoteFields(t *testing.T) {
	observed := time.Now().UTC().Truncate(time.Second)
	valid := observed.Add(time.Hour)
	got := remoteProviderToProto(marketexchange.RemoteProvider{
		Provider: market.Provider{
			ID: "remote", Capacity: 5, Available: 4, PricePerUnit: 9, CostPerUnit: 6,
			MarkupBasisPoints: 100, QuoteID: "remote-q", QuoteVersion: 3,
			ObservedAt: observed, ValidUntil: valid, Models: []string{"m"},
		},
		PeerID: "peer-1",
	})
	if got.GetOrigin() != marketv1.ProviderOrigin_PROVIDER_ORIGIN_REMOTE || got.GetPeerId() != "peer-1" ||
		got.GetCostPerUnit() != 6 || got.GetQuoteId() != "remote-q" || got.GetQuoteVersion() != 3 {
		t.Fatalf("remote quote mapping = %+v", got)
	}
	if !got.GetObservedAt().AsTime().Equal(observed) || !got.GetValidUntil().AsTime().Equal(valid) {
		t.Fatalf("remote quote times = %s/%s", got.GetObservedAt(), got.GetValidUntil())
	}
}
