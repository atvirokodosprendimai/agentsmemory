package contractaxis

import (
	"encoding/json"
	"fmt"
	"io"
	"sort"
	"time"
)

// WriteReport writes every axis and residual in deterministic order.
func WriteReport(w io.Writer, report Report) error {
	if _, err := fmt.Fprintf(w, "CONTRACT AXES: %s\n", report.Status); err != nil {
		return err
	}
	axes := append([]AxisReport(nil), report.Axes...)
	sort.SliceStable(axes, func(i, j int) bool { return axes[i].Axis < axes[j].Axis })
	for _, axis := range axes {
		if _, err := fmt.Fprintf(w, "%s: %s maturity=%s universe=%d cases=%d mutants=%d residuals=%d\n",
			axis.Axis, axis.Status, axis.Maturity, axis.Universe, axis.Cases, len(axis.Mutants), len(axis.Residuals)); err != nil {
			return err
		}
		mutants := append([]MutantEvidence(nil), axis.Mutants...)
		sort.SliceStable(mutants, func(i, j int) bool { return mutants[i].id < mutants[j].id })
		for _, mutant := range mutants {
			paths, err := json.Marshal(mutant.paths)
			if err != nil {
				return fmt.Errorf("marshal mutation paths: %w", err)
			}
			state := "INVALID"
			if mutant.VerifiedFor(axis.Axis, axis.MutationTarget) {
				state = "VERIFIED"
			}
			// ⚠ `expects=`, NOT `failure=`, AND THE RENAME IS THE WHOLE FIX FOR #400.
			// expectedFailure is what the mutation DECLARES it must break — read from
			// the spec before anything runs. Printed as `failure=` it read as an
			// OBSERVATION, and on an INVALID mutant that is the only populated field
			// on the line: a dirty tree makes RunMutation refuse at the clean
			// precondition, so patch, paths, compile and assertion all come back
			// empty and the declaration is left standing alone. A reviewer on `main`
			// with one uncommitted file read
			// `failure="live tools/list lost registration policy"` as a live
			// regression and spent a session on it, with the true cause — `OPEN …
			// repository must be clean before mutation` — filed below it as a
			// residual. The verdict was right and the vocabulary was wrong.
			if _, err := fmt.Fprintf(w,
				"  - MUTANT %s %s axis=%s item=%s case=%s repo=%q head=%s patch=%s paths=%s compile=%s assertion=%s expects=%q\n",
				mutant.id, state, mutant.axis, mutant.item, mutant.caseID, mutant.target.repository,
				mutant.target.head, mutant.patchDigest, paths, mutant.compile,
				mutant.assertion, mutant.expectedFailure,
			); err != nil {
				return err
			}
		}
		residuals := append([]Residual(nil), axis.Residuals...)
		sort.SliceStable(residuals, func(i, j int) bool {
			return lessResidualKey(residuals[i].Key, residuals[j].Key)
		})
		for _, residual := range residuals {
			marker := "OPEN"
			metadata := ""
			if residual.Excepted {
				marker = "EXCEPTED"
				metadata = exceptionMetadata(residual.Exception)
			} else if residual.Obligation != nil {
				marker = "RATCHET"
				metadata = ratchetMetadata(residual.Obligation)
			}
			if _, err := fmt.Fprintf(w, "  - %s %s: %s%s\n", marker, residual.Key.String(), residual.Detail, metadata); err != nil {
				return err
			}
		}
	}
	return nil
}

func exceptionMetadata(exception *Exception) string {
	if exception == nil {
		return ""
	}
	lifetime := "expires=" + exception.Expires.UTC().Format(time.RFC3339)
	if exception.Expires.IsZero() {
		lifetime = "permanent=" + exception.PermanentReason
	}
	return fmt.Sprintf(" [kind=%s owner=%s reference=%s %s reason=%s]",
		exception.Kind, exception.Owner, exception.Reference, lifetime, exception.Reason)
}

func ratchetMetadata(obligation *RatchetObligation) string {
	if obligation == nil {
		return ""
	}
	return fmt.Sprintf(" [owner=%s reference=%s expires=%s reason=%s]",
		obligation.Owner, obligation.Reference, obligation.Expires.UTC().Format(time.RFC3339), obligation.Reason)
}
