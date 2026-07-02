package views

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

// TestManageCardRendersPortalAction renders the ManageCard fragment and checks it
// offers an admin a "Manage subscription" action that posts to the workspace's
// billing/manage endpoint, shows the active plan name, and carries the datastar
// in-flight guard (indicator + disabled) so a double-click can't open two portal
// sessions. Rendering the fragment directly verifies the markup without booting the
// server or seeding a paid workspace.
func TestManageCardRendersPortalAction(t *testing.T) {
	var buf bytes.Buffer
	vm := ProjectVM{TeamID: "t42", PlanName: "Pro", CanManage: true}
	if err := ManageCard(vm).Render(context.Background(), &buf); err != nil {
		t.Fatalf("render: %v", err)
	}
	html := buf.String()

	for _, want := range []string{
		"@post(",                         // hands the browser off via a datastar action
		"/projects/t42/billing/manage",   // ...to this workspace's portal handler
		"Manage subscription",            // the primary action label
		"Pro",                            // the active plan name in the status line
		"data-indicator:managing",        // in-flight signal
		`data-attr:disabled="$managing"`, // double-submit guard
	} {
		if !strings.Contains(html, want) {
			t.Errorf("ManageCard missing %q\n---\n%s", want, html)
		}
	}
}
