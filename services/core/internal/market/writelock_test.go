package market

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A write-lock site that takes mu directly is a stall the node cannot see.
//
// The watchdog in internal/node can only report a holder that RECORDED itself,
// and recording happens in lockWrite. A method that reaches for mu.Lock()
// straight - the obvious thing to write, and what three of these did until
// recently - reintroduces exactly the silence that made the bridge deadlock
// take a SIGQUIT to find.
//
// PARSED, NOT GREPPED. The first version of this test compared trimmed lines
// against the literal "l.mu.Lock()", which is a guard that only catches the
// mistake already made: `func (led *Ledger) Burn(...) { led.mu.Lock() ... }`
// sails through it, gofmt-clean and vet-clean. Matching source text for a
// semantic property is how a test comes to assert nothing.
func TestNoWriteLockSiteBypassesTheWatchdog(t *testing.T) {
	fset := token.NewFileSet()
	pkg, err := parser.ParseDir(fset, ".", func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, 0)
	if err != nil {
		t.Fatalf("parse package market: %v", err)
	}

	// The two functions that are ALLOWED to touch the mutex, because they are
	// the ones that record the holder.
	allowed := map[string]bool{"lockWrite": true, "unlockWrite": true}
	// Read-lock sites are not the watchdog's business: a reader records nothing
	// and a parked one is caught by the queued-writer signal instead.
	writeOps := map[string]bool{"Lock": true, "Unlock": true}

	var offenders []string
	for _, p := range pkg {
		for _, file := range p.Files {
			for _, decl := range file.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if !ok || fn.Body == nil || allowed[fn.Name.Name] {
					continue
				}
				ast.Inspect(fn.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					// Looking for <x>.mu.Lock() / <x>.mu.Unlock(), whatever <x> is
					// called. The receiver name is deliberately not assumed.
					op, ok := call.Fun.(*ast.SelectorExpr)
					if !ok || !writeOps[op.Sel.Name] {
						return true
					}
					field, ok := op.X.(*ast.SelectorExpr)
					if !ok || field.Sel.Name != "mu" {
						return true
					}
					pos := fset.Position(call.Pos())
					offenders = append(offenders, fn.Name.Name+" at "+
						filepath.Base(pos.Filename)+":"+itoa(pos.Line))
					return true
				})
			}
		}
	}
	if len(offenders) > 0 {
		t.Fatalf("these take the ledger write lock without recording the holder, so a stall "+
			"in them is invisible to the node's watchdog - use lockWrite/unlockWrite:\n  %s",
			strings.Join(offenders, "\n  "))
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b []byte
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	return string(b)
}

// A snapshot must not exclude other snapshots.
//
// Reconcile is reachable from an unauthenticated GetBridgeReconciliation, and
// it took the WRITE lock for a read-only backing check - so anyone who could
// poll a read serialized every block the node was applying.
func TestReadOnlySectionsDoNotExcludeEachOther(t *testing.T) {
	store := newTestStore(t)
	l := NewLedger(store)

	inFirst := make(chan struct{})
	secondDone := make(chan struct{})
	release := make(chan struct{})

	go func() {
		_ = l.ReadOnly(func(LedgerTx) error {
			close(inFirst)
			<-release
			return nil
		})
	}()
	<-inFirst
	go func() {
		defer close(secondDone)
		_ = l.ReadOnly(func(LedgerTx) error { return nil })
	}()

	select {
	case <-secondDone:
	case <-time.After(5 * time.Second):
		close(release)
		t.Fatal("a second ReadOnly blocked behind the first: snapshots are excluding each " +
			"other, so an unauthenticated read can still serialize the node")
	}
	close(release)
}

// A ReadOnly section holds only the read lock, which admits other readers, so a
// write through it would race them. It must refuse rather than corrupt.
func TestReadOnlyRefusesToWrite(t *testing.T) {
	store := newTestStore(t)
	l := NewLedger(store)
	if err := l.Credit("a", 100); err != nil {
		t.Fatalf("credit: %v", err)
	}
	if err := l.ReadOnly(func(tx LedgerTx) error { return tx.Transfer("a", "b", 10) }); err == nil {
		t.Fatal("a Transfer inside a ReadOnly section was allowed, which writes the store " +
			"under a lock that admits concurrent readers")
	}
	bal, _ := l.Balance("a")
	if bal != 100 {
		t.Fatalf("balance = %d, want 100 unchanged after a refused write", bal)
	}
}

// A writer must still be excluded, or the snapshot is not one.
func TestReadOnlyExcludesAWriter(t *testing.T) {
	store := newTestStore(t)
	l := NewLedger(store)

	inRead := make(chan struct{})
	release := make(chan struct{})
	wrote := make(chan struct{})

	go func() {
		_ = l.ReadOnly(func(LedgerTx) error {
			close(inRead)
			<-release
			return nil
		})
	}()
	<-inRead
	go func() {
		_ = l.Credit("a", 1)
		close(wrote)
	}()

	select {
	case <-wrote:
		t.Fatal("a writer proceeded while a ReadOnly snapshot was open, so the snapshot " +
			"could straddle a block")
	case <-time.After(200 * time.Millisecond):
	}
	close(release)
	select {
	case <-wrote:
	case <-time.After(5 * time.Second):
		t.Fatal("the writer never ran after the snapshot closed")
	}
}

// LockStatus has to see a queued writer, because that is the whole reader-stall
// signal: nobody holds the write lock and nobody can get it.
func TestAQueuedWriterIsCounted(t *testing.T) {
	store := newTestStore(t)
	l := NewLedger(store)

	inRead := make(chan struct{})
	release := make(chan struct{})
	go func() {
		_ = l.ReadOnly(func(LedgerTx) error {
			close(inRead)
			<-release
			return nil
		})
	}()
	<-inRead

	if st := l.LockStatus(); st.WritersWaiting != 0 {
		t.Fatalf("WritersWaiting = %d before any writer queued", st.WritersWaiting)
	}
	wrote := make(chan struct{})
	go func() { defer close(wrote); _ = l.Credit("a", 1) }()

	// The writer must be joined before this returns, or t.Cleanup closes the
	// store while it is still queued and pebble panics on the next Get.
	defer func() {
		close(release)
		<-wrote
	}()

	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		st := l.LockStatus()
		if st.WritersWaiting == 1 {
			if st.Held {
				t.Fatal("the write lock reports as held while only a reader holds it")
			}
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("a writer blocked behind a reader was never counted, so a parked reader would " +
		"be invisible to the watchdog")
}
