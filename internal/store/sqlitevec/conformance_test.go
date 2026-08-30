package sqlitevec

import (
	"testing"

	"github.com/atvirokodosprendimai/agentsmemory/internal/store"
	"github.com/atvirokodosprendimai/agentsmemory/internal/store/storetest"
)

// TestSQLiteVecRunsTheConformanceSuite runs the shared suite against the real
// migrated schema, which is what the source of truth actually stores into.
func TestSQLiteVecRunsTheConformanceSuite(t *testing.T) {
	storetest.RunPointsConformance(t, "sqlitevec", func(t *testing.T) store.VectorStore {
		return newTestStore(t)
	})
}

// The same backend, the count half: a source of truth that cannot count its
// own rows cannot corroborate a rebuild trigger.
func TestSqlitevecRunsTheCountConformanceSuite(t *testing.T) {
	storetest.RunCountConformance(t, "sqlitevec", func(t *testing.T) store.VectorStore {
		return newTestStore(t)
	})
}

// The same backend, the write half.
func TestSqlitevecRunsTheSetPayloadConformanceSuite(t *testing.T) {
	storetest.RunSetPayloadConformance(t, "sqlitevec", func(t *testing.T) store.VectorStore {
		return newTestStore(t)
	})
}
