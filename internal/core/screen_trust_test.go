// SPDX-License-Identifier: AGPL-3.0-or-later

package core

import (
	"context"
	"testing"

	"github.com/Xustalis/OpenPanda/internal/bus"
	"github.com/Xustalis/OpenPanda/internal/ledger"
	"github.com/Xustalis/OpenPanda/internal/trust"
)

// Screen capture is the most sensitive payload class the bus carries: the
// produced artifact IS the executor's display content. The gates below pin
// the design's promise — display pixels never reach a non-mesh peer through
// any path: not the delegate, not the artifact pull, not the relay, not a
// signed grant.

// TestAdmitDelegateScreenOriginGate: a non-mesh origin must never be able to
// commission a screenshot — auto, review and the policy classifier are all
// downstream of this deterministic refusal.
func TestAdmitDelegateScreenOriginGate(t *testing.T) {
	ctx := context.Background()
	c := pinCore(t, "self", "marker")
	mk := func(id, lvl string) {
		t.Helper()
		if _, err := c.db.Exec(`INSERT INTO employee_cache (id, trust) VALUES (?, ?)`, id, lvl); err != nil {
			t.Fatal(err)
		}
	}
	mk("buddy", "guarded")
	mk("stranger", "open")

	screen := bus.TaskDelegatePayload{TaskID: "s1", Requires: []string{"hardware:screen_capture"}}
	specOnly := bus.TaskDelegatePayload{TaskID: "s2",
		SpecJSON: `{"action_spec":{"target_actuator":"hardware:screen_capture","action":"capture"}}`}
	plain := bus.TaskDelegatePayload{TaskID: "s3", Intent: "list files"}

	if v := c.admitDelegate(ctx, "s1", screen, nil, "buddy"); v.admit || v.review || v.code != bus.DeclineCodeTrustRefused {
		t.Fatalf("guarded origin + screen = %+v, want trust_policy_refused", v)
	}
	if v := c.admitDelegate(ctx, "s1b", specOnly, nil, "buddy"); v.admit || v.review {
		t.Fatalf("guarded origin + screen spec = %+v, want refused", v)
	}
	if v := c.admitDelegate(ctx, "s1c", screen, nil, "stranger"); v.admit || v.review {
		t.Fatalf("open origin + screen = %+v, want refused", v)
	}
	// Ordinary work still admits under guarded — the gate is surgical.
	if v := c.admitDelegate(ctx, "s3", plain, nil, "buddy"); !v.admit {
		t.Fatalf("guarded origin + plain task = %+v, want admit", v)
	}
	// Mesh origin with a screen task admits unchanged.
	if v := c.admitDelegate(ctx, "s4", screen, nil, "mesh-peer"); !v.admit {
		t.Fatalf("mesh origin + screen = %+v, want admit", v)
	}
}

// TestScreenOutputPullDenied: task membership is not enough for display
// content — a participant at sub-mesh trust is refused the screen task's
// output artifact even though it may pull everything else it shares. The
// same row serves the fetch path and the push path (from is the requester
// on one, the sender on the other).
func TestScreenOutputPullDenied(t *testing.T) {
	ctx := context.Background()
	c := pinCore(t, "self", "marker")
	participantTask(t, c, "t-shot", "origin", []string{"origin", "self", "guarded-pal", "mesh-pal"})
	participantTask(t, c, "t-plain", "origin", []string{"origin", "self", "guarded-pal", "mesh-pal"})
	if _, err := c.db.Exec(
		`UPDATE tasks SET requires_json='["hardware:screen_capture"]', output_artifact='h-shot' WHERE task_id='t-shot'`); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.Exec(
		`UPDATE tasks SET output_artifact='h-plain' WHERE task_id='t-plain'`); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.Exec(
		`INSERT INTO employee_cache (id, trust) VALUES ('guarded-pal', 'guarded'), ('origin', 'guarded')`); err != nil {
		t.Fatal(err)
	}

	// The screen output refuses a guarded participant AND its upstream
	// delegator — participation and direction do not soften display content.
	if c.artifactTransferAuthorized(ctx, "t-shot", "h-shot", "guarded-pal", nil) {
		t.Fatal("guarded participant was authorized for screen output")
	}
	if c.artifactTransferAuthorized(ctx, "t-shot", "h-shot", "origin", nil) {
		t.Fatal("guarded upstream delegator was authorized for screen output")
	}
	// Mesh participant is unaffected — the rule costs its traffic nothing.
	if !c.artifactTransferAuthorized(ctx, "t-shot", "h-shot", "mesh-pal", nil) {
		t.Fatal("mesh participant was refused screen output")
	}
	// A wrong hash stays bound to nothing for anyone.
	if c.artifactTransferAuthorized(ctx, "t-shot", "h-other", "mesh-pal", nil) {
		t.Fatal("hash not belonging to the task was authorized")
	}
	// The ordinary task's output obeys participation alone — the screen gate
	// is surgical, not a blanket on artifact traffic.
	if !c.artifactTransferAuthorized(ctx, "t-plain", "h-plain", "guarded-pal", nil) {
		t.Fatal("guarded participant was refused an ordinary task's output")
	}
}

// TestScreenEligibleForSend: routing must drop a sub-mesh executor for screen
// work at the candidate-filter step, before the payload is ever assembled —
// and must keep it for ordinary tasks.
func TestScreenEligibleForSend(t *testing.T) {
	ctx := context.Background()
	c := pinCore(t, "self", "marker")
	if _, err := c.db.Exec(
		`INSERT INTO employee_cache (id, trust) VALUES ('guarded-node', 'guarded'), ('mesh-node', '')`); err != nil {
		t.Fatal(err)
	}
	nodes := []ledger.Node{{ID: "guarded-node"}, {ID: "mesh-node"}}

	got := c.eligibleForSend(ctx, trust.Needs{Screen: true}, nodes)
	if len(got) != 1 || got[0].ID != "mesh-node" {
		t.Fatalf("screen needs eligibility = %v, want [mesh-node]", got)
	}
	if got := c.eligibleForSend(ctx, trust.Needs{}, nodes); len(got) != 2 {
		t.Fatalf("ordinary needs eligibility = %v, want both", got)
	}
}

// TestScreenOutputDemotionCutsCachedTransfer: a peer authorized while mesh
// must not keep pulling screen bytes on the cached verdict after a demotion
// — the cached entry re-checks the tier on every hit.
func TestScreenOutputDemotionCutsCachedTransfer(t *testing.T) {
	ctx := context.Background()
	c := pinCore(t, "self", "marker")
	participantTask(t, c, "t-demote", "origin", []string{"origin", "self", "pal"})
	if _, err := c.db.Exec(
		`UPDATE tasks SET requires_json='["hardware:screen_capture"]', output_artifact='h-shot' WHERE task_id='t-demote'`); err != nil {
		t.Fatal(err)
	}
	// A directory row must exist for the demotion pin to land on.
	if _, err := c.db.Exec(`INSERT INTO employee_cache (id) VALUES ('pal')`); err != nil {
		t.Fatal(err)
	}
	if !c.artifactTransferAuthorized(ctx, "t-demote", "h-shot", "pal", nil) {
		t.Fatal("mesh participant should authorize for screen output")
	}
	// Demote mid-transfer: the very next chunk must refuse.
	if err := ledger.SetPeerTrust(c.db, "pal", "guarded"); err != nil {
		t.Fatal(err)
	}
	if c.artifactTransferAuthorized(ctx, "t-demote", "h-shot", "pal", nil) {
		t.Fatal("demoted peer kept pulling screen bytes on the cached verdict")
	}
	// Re-assert mesh restores the pull — the verdict was never poisoned.
	if err := ledger.SetPeerTrust(c.db, "pal", "mesh"); err != nil {
		t.Fatal(err)
	}
	if !c.artifactTransferAuthorized(ctx, "t-demote", "h-shot", "pal", nil) {
		t.Fatal("re-asserted mesh peer was still refused")
	}
}

// TestScreenOutputIndexBound: an output bound to the task through the pool's
// index row (a pull/adoption that never wrote output_artifact) is still
// recognized as the screen task's produced tree — inputs and context are
// ruled out before the index is consulted.
func TestScreenOutputIndexBound(t *testing.T) {
	ctx := context.Background()
	c := pinCore(t, "self", "marker")
	participantTask(t, c, "t-idx", "origin", []string{"origin", "self", "guarded-pal"})
	if _, err := c.db.Exec(
		`UPDATE tasks SET requires_json='["hardware:screen_capture"]' WHERE task_id='t-idx'`); err != nil {
		t.Fatal(err)
	}
	if err := c.store.RecordArtifact(ctx, "h-idxout", 4, "t-idx", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := c.db.Exec(
		`INSERT INTO employee_cache (id, trust) VALUES ('guarded-pal', 'guarded')`); err != nil {
		t.Fatal(err)
	}
	if c.artifactTransferAuthorized(ctx, "t-idx", "h-idxout", "guarded-pal", nil) {
		t.Fatal("index-bound screen output reached a guarded participant")
	}
}

// TestScreenSensitiveCustomActuator: an operator-defined actuator id the
// canonical predicate cannot name is caught two ways — the local card's
// category on the producer itself, and the persisted runtime stamp that a
// flagged task_result leaves on every delegator hop (the relay's card
// cannot classify the id; the stamp is what keeps its verdict honest).
func TestScreenSensitiveCustomActuator(t *testing.T) {
	c := pinCore(t, "self", "marker")
	// Own card: declare a custom screen_sensing actuator and require it.
	c.card.Actuators = append(c.card.Actuators, ledger.ActuatorProfile{
		ID: "acme:display_grab", Category: "screen_sensing", Interface: "desktop",
	})
	if !c.taskScreenSensitive(Task{Requires: []string{"acme:display_grab"}}) {
		t.Fatal("own-card screen_sensing actuator id not recognized")
	}
	if !c.actuatorIsScreen("acme:display_grab") {
		t.Fatal("actuatorIsScreen must read the card's category")
	}
	if c.actuatorIsScreen("acme:plain") {
		t.Fatal("unknown actuator id flagged screen")
	}
	// The stamp: a row whose runtime flagged screen stays sensitive even
	// where no card can name the id — this is the relay/delegator verdict.
	c.card.Actuators = nil
	if c.taskScreenSensitive(Task{Requires: []string{"acme:display_grab"}}) {
		t.Fatal("unclassifiable id must NOT be inferred screen without a stamp")
	}
	if !c.taskScreenSensitive(Task{Requires: []string{"acme:display_grab"}, ScreenOutput: true}) {
		t.Fatal("stamped screen_output row not honored")
	}
	// Canonical fallback survives an empty card.
	if !c.actuatorIsScreen("hardware:screen_capture") {
		t.Fatal("canonical actuator id must classify screen even off-card")
	}
	// A spec-targeted custom id classifies on the producer's own card even
	// when requires does not name it — the residual path if stamping were
	// skipped (pre-flag executor) or its write failed.
	c.card.Actuators = append(c.card.Actuators, ledger.ActuatorProfile{
		ID: "acme:display_grab", Category: "screen_sensing", Interface: "desktop",
	})
	spec := `{"action_spec":{"target_actuator":"acme:display_grab","action":"grab"}}`
	if !c.taskScreenSensitive(Task{SpecJSON: spec}) {
		t.Fatal("spec-targeted custom screen actuator not recognized on own card")
	}
}

// TestScreenNeedsSurfacesAgree: every place a needs value is derived —
// submission input, persisted row, wire payload — must come to the same
// screen verdict or one gate passes what another refuses.
func TestScreenNeedsSurfacesAgree(t *testing.T) {
	screenReq := []string{"hardware:screen_capture"}
	spec := `{"action_spec":{"target_actuator":"hardware:screen_capture","action":"capture"}}`

	if !needsForInput(TaskInput{Requires: screenReq}).Screen {
		t.Fatal("needsForInput missed the screen requires")
	}
	if !needsForInput(TaskInput{SpecJSON: spec}).Screen {
		t.Fatal("needsForInput missed the screen action_spec")
	}
	if !needsForTask(Task{Requires: screenReq}).Screen {
		t.Fatal("needsForTask missed the screen requires")
	}
	if !needsForTask(Task{SpecJSON: spec}).Screen {
		t.Fatal("needsForTask missed the screen action_spec")
	}
	if !trust.NeedsOf(bus.TaskDelegatePayload{Requires: screenReq}).Screen {
		t.Fatal("NeedsOf missed the screen requires")
	}
	if !trust.NeedsOf(bus.TaskDelegatePayload{SpecJSON: spec}).Screen {
		t.Fatal("NeedsOf missed the screen action_spec")
	}
	// And a plain task agrees everywhere that it is plain.
	if needsForInput(TaskInput{Intent: "x"}).Screen ||
		needsForTask(Task{Intent: "x"}).Screen ||
		trust.NeedsOf(bus.TaskDelegatePayload{Intent: "x"}).Screen {
		t.Fatal("ordinary task flagged screen on some surface")
	}
}
