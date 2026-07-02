package web

import (
	"net/http"

	"github.com/atvirokodosprendimai/agentsmemory/internal/billing"
	"github.com/atvirokodosprendimai/agentsmemory/internal/tenant"
	"github.com/atvirokodosprendimai/agentsmemory/internal/web/views"

	"github.com/starfederation/datastar-go/datastar"
)

// upgradeSignals is the datastar payload for the upgrade action. The chosen
// billing option IS the target plan code ("pro_monthly" | "pro_annual"), so the
// segmented control writes the code straight into the signal and the server maps
// it to a Stripe price — no separate interval-to-plan translation.
type upgradeSignals struct {
	ProInterval string `json:"proInterval"`
}

// postUpgrade starts a Stripe hosted-checkout session for the workspace and
// redirects the browser to it. It is admin-gated (only an admin may change what
// the workspace is billed) and refuses anything but a real Pro plan code, so a
// tampered signal can never start a checkout for something we don't sell. Signals
// are read before the SSE stream opens, because starting the stream flushes the
// response and the request body is no longer readable after that.
func (s *Server) postUpgrade(w http.ResponseWriter, r *http.Request) {
	u, teamID, role, ok := s.membership(w, r)
	if !ok {
		return
	}
	var sig upgradeSignals
	_ = datastar.ReadSignals(r, &sig)

	sse := datastar.NewSSE(w, r)
	flash := func(msg string) {
		_ = sse.PatchElementTempl(views.Flash(views.FlashVM{Kind: "error", Message: msg}))
	}

	if !s.billing.Enabled() {
		flash("Upgrades aren't available right now. Please check back soon.")
		return
	}
	if role != tenant.RoleAdmin {
		flash("Only a workspace admin can change the plan.")
		return
	}
	planCode := sig.ProInterval
	if planCode != "pro_monthly" && planCode != "pro_annual" {
		flash("Choose a billing option to continue.")
		return
	}

	base := requestBaseURL(r)
	url, err := s.billing.StartCheckout(r.Context(), billing.CheckoutRequest{
		TeamID:        teamID,
		PlanCode:      planCode,
		CustomerEmail: u.Email,
		SuccessURL:    base + "/projects/" + teamID + "/billing/success",
		CancelURL:     base + "/projects/" + teamID + "/billing/cancel",
	})
	if err != nil {
		// StartCheckout already validated the plan; reaching here is a Stripe-side
		// or transient failure, kept off the page as a generic retry message.
		flash("Could not start checkout. Please try again.")
		return
	}
	// Hand the browser off to Stripe's hosted checkout page.
	_ = sse.Redirect(url)
}

// getBillingSuccess is Stripe's success_url return: the user is back from a
// completed checkout. The plan flip is webhook-driven (never trusted from this
// redirect, which an attacker could forge), so this only confirms receipt and
// re-renders the project page — the Pro badge appears once the webhook lands.
func (s *Server) getBillingSuccess(w http.ResponseWriter, r *http.Request) {
	u, teamID, role, ok := s.membership(w, r)
	if !ok {
		return
	}
	s.renderProjectPage(w, r, u, teamID, role, views.FlashVM{
		Kind:    "success",
		Message: "Payment received — your workspace is upgrading to Pro. It updates within a few moments.",
	})
}

// getBillingCancel is Stripe's cancel_url return: the user backed out of checkout.
// Nothing changed; show a neutral note and re-render the project page.
func (s *Server) getBillingCancel(w http.ResponseWriter, r *http.Request) {
	u, teamID, role, ok := s.membership(w, r)
	if !ok {
		return
	}
	s.renderProjectPage(w, r, u, teamID, role, views.FlashVM{
		Kind:    "note",
		Message: "Checkout canceled — your plan is unchanged.",
	})
}

// postManageSubscription opens the active provider's hosted customer portal for the
// workspace and redirects the admin to it, where they update payment, download
// invoices, or cancel. Admin-gated (only an admin manages billing). Any cancellation
// is applied by the provider webhook, so this handler only hands the browser off to
// the portal — it never changes the plan itself.
func (s *Server) postManageSubscription(w http.ResponseWriter, r *http.Request) {
	_, teamID, role, ok := s.membership(w, r)
	if !ok {
		return
	}
	sse := datastar.NewSSE(w, r)
	flash := func(msg string) {
		_ = sse.PatchElementTempl(views.Flash(views.FlashVM{Kind: "error", Message: msg}))
	}
	if !s.billing.Enabled() {
		flash("Billing isn't available right now.")
		return
	}
	if role != tenant.RoleAdmin {
		flash("Only a workspace admin can manage the plan.")
		return
	}
	url, err := s.billing.ManageURL(r.Context(), teamID, requestBaseURL(r)+"/projects/"+teamID)
	if err != nil {
		// ErrNoSubscription (nothing to manage) or a provider/transient failure — kept
		// off the page as a generic retry message.
		flash("Couldn't open the billing portal. Please try again.")
		return
	}
	// Hand the browser off to the provider's hosted portal.
	_ = sse.Redirect(url)
}

// renderProjectPage renders the full project page with an optional flash. It is
// the shared assembly behind getProject (empty flash) and the billing
// success/cancel returns, so the page is built in exactly one place.
func (s *Server) renderProjectPage(w http.ResponseWriter, r *http.Request, u tenant.User, teamID string, role tenant.Role, flash views.FlashVM) {
	proj, found := s.projectVM(r.Context(), u.ID, teamID)
	if !found {
		http.NotFound(w, r)
		return
	}
	summaries, err := s.skills.List(r.Context(), teamID)
	if err != nil {
		http.Error(w, "could not load skills", http.StatusInternalServerError)
		return
	}
	s.render(w, r, views.ProjectDetailPage(views.ProjectDetailData{
		UserEmail:  u.Email,
		Project:    proj,
		Skills:     toSkillVMs(summaries),
		CanWrite:   webSkillCaller{role: role}.CanWrite(),
		ServerBase: requestBaseURL(r),
		Share:      s.buildShareData(r.Context(), u, teamID, role),
		Merge:      s.buildMergeData(r.Context(), u, teamID, role),
		Flash:      flash,
	}))
}
