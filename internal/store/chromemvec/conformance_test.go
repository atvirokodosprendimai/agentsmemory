package chromemvec

import (
	"path/filepath"
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/storetest"
)

// TestChromemVecRunsTheConformanceSuite runs the shared suite against the
// local-default index.
func TestChromemVecRunsTheConformanceSuite(t *testing.T) {
	storetest.RunPointsConformance(t, "chromemvec", func(t *testing.T) store.VectorStore {
		idx, err := New(filepath.Join(t.TempDir(), "chromem"))
		if err != nil {
			t.Fatalf("open index: %v", err)
		}
		return idx
	})
}

// The same backend, the write half.
func TestChromemvecRunsTheSetPayloadConformanceSuite(t *testing.T) {
	storetest.RunCountConformance(t, "chromemvec", func(t *testing.T) store.VectorStore {
		idx, err := New(filepath.Join(t.TempDir(), "chromem"))
		if err != nil {
			t.Fatalf("open index: %v", err)
		}
		return idx
	})
	storetest.RunSetPayloadConformance(t, "chromemvec", func(t *testing.T) store.VectorStore {
		idx, err := New(filepath.Join(t.TempDir(), "chromem"))
		if err != nil {
			t.Fatalf("open index: %v", err)
		}
		return idx
	})
}
