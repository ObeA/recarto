package graph

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/gerwim/recarto/internal/store"
)

// heldReadSource returns each file's bytes as they are when Read is called, then
// holds the call until the test sends on release: an index that parsed the file
// just before an edit.
type heldReadSource struct {
	inner   Source
	read    chan string   // receives the path of each Read
	release chan struct{} // one send per Read
}

func newHeldReadSource(root string) heldReadSource {
	return heldReadSource{inner: FSSource{Root: root}, read: make(chan string), release: make(chan struct{})}
}

func (h heldReadSource) List() ([]FileEntry, error) { return h.inner.List() }
func (h heldReadSource) Read(p string) ([]byte, error) {
	b, err := h.inner.Read(p)
	h.read <- p
	<-h.release
	return b, err
}

// awaitRead waits for the source's next Read.
func (h heldReadSource) awaitRead(t *testing.T) {
	t.Helper()
	select {
	case <-h.read:
	case <-time.After(hangTimeout):
		t.Fatal("no read came")
	}
}

// writeGo writes rel in wt defining fn and returns its bytes and stat, as the
// post-edit hook passes them.
func writeGo(t *testing.T, wt, rel, fn string) ([]byte, int64, int64) {
	t.Helper()
	src := []byte("package p\n\nfunc " + fn + "() {}\n")
	p := filepath.Join(wt, rel)
	if err := os.WriteFile(p, src, 0o644); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(p)
	if err != nil {
		t.Fatal(err)
	}
	return src, info.ModTime().UnixMilli(), info.Size()
}

// hasSymbol reports whether e's graph of wt holds a symbol named name.
func hasSymbol(t *testing.T, e *Engine, wt, name string) bool {
	t.Helper()
	var n int
	if err := e.st.DB.QueryRow(`SELECT COUNT(*) FROM nodes n JOIN files f ON f.id = n.file_id
		WHERE f.worktree = ? AND n.name = ?`, wt, name).Scan(&n); err != nil {
		t.Fatal(err)
	}
	return n > 0
}

func TestEditDeferredWhileIndexingIsInWhenTheIndexEnds(t *testing.T) {
	e1, e2 := twoEngines(t)
	ctx := context.Background()
	wt := t.TempDir()
	writeGo(t, wt, "a.go", "Old")
	src := newHeldReadSource(wt)
	done1 := make(chan error, 1)
	go func() { done1 <- e1.ReconcileIfStale(ctx, wt, src, "") }()
	src.awaitRead(t) // e1 holds the index lock and the bytes of Old

	// Hold the store's write lock, as the indexer's resolve does.
	inTx, endTx, txDone := make(chan struct{}), make(chan struct{}), make(chan error, 1)
	go func() {
		txDone <- e1.st.WriteTx(ctx, func(store.Conn) error {
			close(inTx)
			<-endTx
			return nil
		})
	}()
	select {
	case <-inTx:
	case <-time.After(hangTimeout):
		t.Fatal("write transaction never started")
	}

	b, mtime, size := writeGo(t, wt, "a.go", "NewName")
	edit := make(chan error, 1)
	go func() { edit <- e2.ReindexFileIncremental(ctx, wt, "a.go", b, mtime, size) }()
	awaitErr(t, "post-edit while the indexer holds the write lock", edit)
	close(endTx)
	awaitErr(t, "held write transaction", txDone)
	if hasSymbol(t, e2, wt, "NewName") {
		t.Fatal("the post-edit wrote graph rows beside the running index")
	}

	src.release <- struct{}{}
	src.awaitRead(t) // the drain pass reads the edit
	src.release <- struct{}{}
	awaitErr(t, "index", done1)
	if !hasSymbol(t, e1, wt, "NewName") || hasSymbol(t, e1, wt, "Old") {
		t.Errorf("after the index: NewName=%v Old=%v; want the edit in", hasSymbol(t, e1, wt, "NewName"), hasSymbol(t, e1, wt, "Old"))
	}
}

func TestEditDeferredDuringTheDrainPassIsIn(t *testing.T) {
	e1, e2 := twoEngines(t)
	ctx := context.Background()
	wt := t.TempDir()
	writeGo(t, wt, "a.go", "Old")
	src := newHeldReadSource(wt)
	done1 := make(chan error, 1)
	go func() { done1 <- e1.ReconcileIfStale(ctx, wt, src, "tok") }()
	src.awaitRead(t)

	edit := func(fn string) {
		b, mtime, size := writeGo(t, wt, "a.go", fn)
		if err := e2.ReindexFileIncremental(ctx, wt, "a.go", b, mtime, size); err != nil {
			t.Fatal(err)
		}
	}
	edit("Second")
	src.release <- struct{}{}
	src.awaitRead(t) // the drain pass for Second
	edit("ThirdOne")
	src.release <- struct{}{}
	src.awaitRead(t) // the next drain pass
	src.release <- struct{}{}
	awaitErr(t, "index", done1)
	if !hasSymbol(t, e1, wt, "ThirdOne") {
		t.Error("an edit deferred during the drain pass is not in the graph")
	}
}

func TestPostEditTakesInAMarkNobodyDrained(t *testing.T) {
	e := newTestEngine(t)
	ctx := context.Background()
	wt := t.TempDir()
	writeGo(t, wt, "a.go", "A")
	writeGo(t, wt, "b.go", "B")
	if err := e.ReconcileIfStale(ctx, wt, FSSource{Root: wt}, ""); err != nil {
		t.Fatal(err)
	}
	// A post-edit whose try-lock failed just before the holder released and ran
	// its last check.
	writeGo(t, wt, "b.go", "Later")
	if err := e.markDeferred(wt); err != nil {
		t.Fatal(err)
	}

	b, mtime, size := writeGo(t, wt, "a.go", "AA")
	if err := e.ReindexFileIncremental(ctx, wt, "a.go", b, mtime, size); err != nil {
		t.Fatal(err)
	}
	if !hasSymbol(t, e, wt, "Later") {
		t.Error("the marked edit of b.go is not in the graph")
	}
}
