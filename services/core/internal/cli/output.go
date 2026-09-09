package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	marketv1 "github.com/ecirlabs/matrix-proto/gen/go/matrix/market/v1"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// printJSON writes v as indented JSON to w.
func printJSON(w io.Writer, v interface{}) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// providerRow is the human/JSON view of a provider.
type providerRow struct {
	ID                string   `json:"id"`
	Capacity          uint64   `json:"capacity"`
	Available         uint64   `json:"available"`
	PricePerUnit      uint64   `json:"price_per_unit"`
	CostPerUnit       uint64   `json:"cost_per_unit"`
	MarkupBasisPoints uint32   `json:"markup_basis_points"`
	QuoteID           string   `json:"quote_id"`
	QuoteVersion      uint64   `json:"quote_version"`
	ObservedAt        string   `json:"observed_at"`
	ValidUntil        string   `json:"valid_until"`
	Origin            string   `json:"origin"`
	PeerID            string   `json:"peer_id,omitempty"`
	Models            []string `json:"models,omitempty"`
}

func providerToRow(p *marketv1.Provider) providerRow {
	return providerRow{
		ID:                p.GetId(),
		Capacity:          p.GetCapacity(),
		Available:         p.GetAvailable(),
		PricePerUnit:      p.GetPricePerUnit(),
		CostPerUnit:       p.GetCostPerUnit(),
		MarkupBasisPoints: p.GetMarkupBasisPoints(),
		QuoteID:           p.GetQuoteId(),
		QuoteVersion:      p.GetQuoteVersion(),
		ObservedAt:        timestampString(p.GetObservedAt()),
		ValidUntil:        timestampString(p.GetValidUntil()),
		Origin:            originString(p.GetOrigin()),
		PeerID:            p.GetPeerId(),
		Models:            p.GetModels(),
	}
}

func timestampString(ts *timestamppb.Timestamp) string {
	if ts == nil {
		return ""
	}
	return ts.AsTime().UTC().Format(time.RFC3339)
}

func originString(o marketv1.ProviderOrigin) string {
	switch o {
	case marketv1.ProviderOrigin_PROVIDER_ORIGIN_LOCAL:
		return "local"
	case marketv1.ProviderOrigin_PROVIDER_ORIGIN_REMOTE:
		return "remote"
	default:
		return "unspecified"
	}
}

// jobRow is the human/JSON view of a job.
type jobRow struct {
	ID              string `json:"id"`
	Buyer           string `json:"buyer"`
	Provider        string `json:"provider"`
	Units           uint64 `json:"units"`
	Price           uint64 `json:"price"`
	PricePerUnit    uint64 `json:"price_per_unit"`
	QuoteID         string `json:"quote_id"`
	QuoteVersion    uint64 `json:"quote_version"`
	QuoteObservedAt string `json:"quote_observed_at"`
	QuoteValidUntil string `json:"quote_valid_until"`
	Status          string `json:"status"`
}

func jobToRow(j *marketv1.Job) jobRow {
	return jobRow{
		ID:              j.GetId(),
		Buyer:           j.GetBuyer(),
		Provider:        j.GetProvider(),
		Units:           j.GetUnits(),
		Price:           j.GetPrice(),
		PricePerUnit:    j.GetPricePerUnit(),
		QuoteID:         j.GetQuoteId(),
		QuoteVersion:    j.GetQuoteVersion(),
		QuoteObservedAt: timestampString(j.GetQuoteObservedAt()),
		QuoteValidUntil: timestampString(j.GetQuoteValidUntil()),
		Status:          jobStatusString(j.GetStatus()),
	}
}

func jobStatusString(s marketv1.JobStatus) string {
	switch s {
	case marketv1.JobStatus_JOB_STATUS_PENDING:
		return "pending"
	case marketv1.JobStatus_JOB_STATUS_RUNNING:
		return "running"
	case marketv1.JobStatus_JOB_STATUS_COMPLETED:
		return "completed"
	case marketv1.JobStatus_JOB_STATUS_FAILED:
		return "failed"
	case marketv1.JobStatus_JOB_STATUS_CANCELLED:
		return "cancelled"
	default:
		return "unspecified"
	}
}

// txRow is the human/JSON view of a committed transfer in the consensus
// transaction history.
type txRow struct {
	Index       uint64 `json:"index"`
	From        string `json:"from"`
	To          string `json:"to"`
	Amount      uint64 `json:"amount"`
	Nonce       uint64 `json:"nonce"`
	BlockHeight uint64 `json:"block_height"`
}

func txToRow(t *marketv1.Transaction) txRow {
	return txRow{
		Index:       t.GetIndex(),
		From:        t.GetFrom(),
		To:          t.GetTo(),
		Amount:      t.GetAmount(),
		Nonce:       t.GetNonce(),
		BlockHeight: t.GetBlockHeight(),
	}
}

// newTabWriter returns a tabwriter configured for aligned table output.
func newTabWriter(w io.Writer) *tabwriter.Writer {
	return tabwriter.NewWriter(w, 0, 4, 2, ' ', 0)
}

// printProviders renders providers as JSON or a table.
func printProviders(w io.Writer, asJSON bool, provs []*marketv1.Provider) error {
	rows := make([]providerRow, 0, len(provs))
	for _, p := range provs {
		rows = append(rows, providerToRow(p))
	}
	if asJSON {
		return printJSON(w, rows)
	}
	tw := newTabWriter(w)
	fmt.Fprintln(tw, "ID\tCAPACITY\tAVAILABLE\tPRICE/UNIT\tCOST/UNIT\tMARKUP(BPS)\tQUOTE\tVERSION\tOBSERVED\tVALID UNTIL\tORIGIN\tPEER\tMODELS")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%d\t%d\t%d\t%d\t%d\t%s\t%d\t%s\t%s\t%s\t%s\t%s\n",
			r.ID, r.Capacity, r.Available, r.PricePerUnit, r.CostPerUnit, r.MarkupBasisPoints,
			r.QuoteID, r.QuoteVersion, r.ObservedAt, r.ValidUntil, r.Origin, r.PeerID,
			strings.Join(r.Models, ","))
	}
	return tw.Flush()
}

// printJobs renders jobs as JSON or a table.
func printJobs(w io.Writer, asJSON bool, jobs []*marketv1.Job) error {
	rows := make([]jobRow, 0, len(jobs))
	for _, j := range jobs {
		rows = append(rows, jobToRow(j))
	}
	if asJSON {
		return printJSON(w, rows)
	}
	tw := newTabWriter(w)
	fmt.Fprintln(tw, "ID\tBUYER\tPROVIDER\tUNITS\tPRICE/UNIT\tPRICE\tQUOTE\tVERSION\tVALID UNTIL\tSTATUS")
	for _, r := range rows {
		fmt.Fprintf(tw, "%s\t%s\t%s\t%d\t%d\t%d\t%s\t%d\t%s\t%s\n", r.ID, r.Buyer, r.Provider,
			r.Units, r.PricePerUnit, r.Price, r.QuoteID, r.QuoteVersion, r.QuoteValidUntil, r.Status)
	}
	return tw.Flush()
}

// printJob renders a single job as JSON or key/value lines.
func printJob(w io.Writer, asJSON bool, j *marketv1.Job) error {
	r := jobToRow(j)
	if asJSON {
		return printJSON(w, r)
	}
	fmt.Fprintf(w, "ID:       %s\n", r.ID)
	fmt.Fprintf(w, "Buyer:    %s\n", r.Buyer)
	fmt.Fprintf(w, "Provider: %s\n", r.Provider)
	fmt.Fprintf(w, "Units:       %d\n", r.Units)
	fmt.Fprintf(w, "Price/unit:  %d\n", r.PricePerUnit)
	fmt.Fprintf(w, "Price:       %d\n", r.Price)
	fmt.Fprintf(w, "Quote:       %s/%d\n", r.QuoteID, r.QuoteVersion)
	fmt.Fprintf(w, "Observed:    %s\n", r.QuoteObservedAt)
	fmt.Fprintf(w, "Valid until: %s\n", r.QuoteValidUntil)
	fmt.Fprintf(w, "Status:      %s\n", r.Status)
	return nil
}

// printTransactions renders committed transfers as JSON or a table. total is
// the number of committed transfers in the consensus history.
func printTransactions(w io.Writer, asJSON bool, txs []*marketv1.Transaction, total uint64) error {
	rows := make([]txRow, 0, len(txs))
	for _, t := range txs {
		rows = append(rows, txToRow(t))
	}
	if asJSON {
		return printJSON(w, struct {
			Total        uint64  `json:"total"`
			Transactions []txRow `json:"transactions"`
		}{Total: total, Transactions: rows})
	}
	tw := newTabWriter(w)
	fmt.Fprintln(tw, "INDEX\tFROM\tTO\tAMOUNT\tNONCE\tBLOCK")
	for _, r := range rows {
		fmt.Fprintf(tw, "%d\t%s\t%s\t%d\t%d\t%d\n", r.Index, r.From, r.To, r.Amount, r.Nonce, r.BlockHeight)
	}
	if err := tw.Flush(); err != nil {
		return err
	}
	fmt.Fprintf(w, "transfers: %d\n", total)
	return nil
}

// printTransaction renders a single committed transfer as JSON or key/value
// lines.
func printTransaction(w io.Writer, asJSON bool, t *marketv1.Transaction) error {
	r := txToRow(t)
	if asJSON {
		return printJSON(w, r)
	}
	fmt.Fprintf(w, "Index:  %d\n", r.Index)
	fmt.Fprintf(w, "From:   %s\n", r.From)
	fmt.Fprintf(w, "To:     %s\n", r.To)
	fmt.Fprintf(w, "Amount: %d\n", r.Amount)
	fmt.Fprintf(w, "Nonce:  %d\n", r.Nonce)
	fmt.Fprintf(w, "Block:  %d\n", r.BlockHeight)
	return nil
}
