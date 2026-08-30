package main

import (
	"context"
	"log"
	"net/http"
	"time"

	"github.com/atvirokodosprendimai/agentsmemory/internal/billing"

	"gorm.io/gorm"
)

// ocTierPlanCodes maps Open Collective's tier slugs onto our sellable plan codes.
//
// The slugs are the ones already embedded in the OPENCOLLECTIVE_CHECKOUT_* URLs an
// operator configures — .../contribute/pro-monthly-104934/checkout — and were
// confirmed against the live API on 2026-08-28, which reported exactly two tiers on
// the ai-agents-memory project: pro-monthly at EUR 50/month and pro-yearly at EUR
// 500/year.
//
// Keyed on the slug rather than the numeric tier id because only the slug is in the
// PUBLISHED schema: `Tier.legacyId` resolves but is absent from introspection on
// both production and staging, so it is a working field with no contract behind it.
//
// ⚠ A contribution naming any OTHER tier, or no tier at all, is an ordinary
// donation. The reconciler ignores it rather than guessing a plan from the amount,
// because guessing would let a EUR 5 one-off buy a EUR 50/month subscription.
var ocTierPlanCodes = map[string]string{
	"pro-monthly": "pro_monthly",
	"pro-yearly":  "pro_annual",
}

// ocReconcileHTTPTimeout bounds a single GraphQL call. The loop owns its own
// client so a hung provider cannot wedge the goroutine: without this the request
// would inherit no deadline and one bad connection would stop every future pass.
const ocReconcileHTTPTimeout = 30 * time.Second

// startOpenCollectiveReconciler is the line that makes ADR-042 reachable. Every
// other part of that decision — the intent store, the order source, the reconciler —
// is finished, tested, and activates nothing until this runs, which is precisely
// this repo's signature defect (AGENTS.md §Reachability).
// TestOpenCollectiveActivationIsReachable fails when this call, or the construction
// inside it, is removed.
//
// It starts NOTHING unless the deployment is actually running OpenCollective with a
// personal token and a collective slug. That is the rollback path in the ADR: unset
// the token and reconciliation stops, with no code change and no redeploy beyond the
// environment. Each missing precondition is logged by name, because "billing is
// quiet" and "billing is misconfigured" must not look the same in a log.
func startOpenCollectiveReconciler(ctx context.Context, cfg billing.Config, svc *billing.Service, gdb *gorm.DB) {
	if cfg.Provider != billing.ProviderOpenCollective {
		return
	}
	switch {
	case cfg.OpenCollectivePersonalToken == "":
		log.Printf("billing: opencollective reconciliation OFF (OPENCOLLECTIVE_PERSONAL_TOKEN unset) — a paid contribution will NOT activate a plan; activate with the set-plan CLI")
		return
	case cfg.OpenCollectiveSlug == "":
		log.Printf("billing: opencollective reconciliation OFF (OPENCOLLECTIVE_COLLECTIVE_SLUG unset) — a paid contribution will NOT activate a plan; activate with the set-plan CLI")
		return
	}

	orders := billing.NewOCOrderSource(
		&http.Client{Timeout: ocReconcileHTTPTimeout},
		cfg.OpenCollectiveAPIURL, cfg.OpenCollectivePersonalToken, cfg.OpenCollectiveSlug,
	)
	// The ledger is what keeps a POLL idempotent over time: without it every pass
	// re-applies a decision the provider has not changed, which silently reverted an
	// operator's `set-plan` downgrade fifteen minutes later.
	rec := billing.NewReconciler(svc, orders, billing.NewIntentRepo(gdb), ocTierPlanCodes).
		WithLedger(billing.NewAppliedOrderRepo(gdb))

	log.Printf("billing: opencollective reconciliation ON — polling %s for collective %q every %s",
		cfg.OpenCollectiveAPIURL, cfg.OpenCollectiveSlug, cfg.ReconcileInterval)
	// Bound to the server's lifecycle context, so shutdown stops the loop rather
	// than leaking a goroutine that outlives the process's intent to run.
	go rec.Run(ctx, cfg.ReconcileInterval)
}
