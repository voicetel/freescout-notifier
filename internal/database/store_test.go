package database

import (
	"context"
	"path/filepath"
	"testing"
	"time"

	"github.com/voicetel/freescout-notifier/internal/models"
)

func newTestStore(t *testing.T) *Store {
	t.Helper()
	store, err := NewStore(filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	t.Cleanup(func() { store.Close() })
	return store
}

func testAlert(ticketID int, typ models.NotificationType, reminder int) models.Alert {
	now := time.Now().UTC().Truncate(time.Second)
	return models.Alert{
		Type: typ,
		Ticket: models.Ticket{
			ID:             ticketID,
			Number:         ticketID + 1000,
			Subject:        "Test subject",
			CustomerName:   "Jane Doe",
			WaitingSince:   now.Add(-2 * time.Hour),
			CreatedAt:      now.Add(-3 * time.Hour),
			MinutesWaiting: 120,
		},
		ReminderNumber: reminder,
	}
}

func TestStoreReopensExistingFile(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "reopen.db")

	store, err := NewStore(path)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	if err := store.MarkEventSeen(ctx, "conversation:1", 1); err != nil {
		t.Fatalf("MarkEventSeen: %v", err)
	}
	store.Close()

	store, err = NewStore(path)
	if err != nil {
		t.Fatalf("reopen NewStore: %v", err)
	}
	defer store.Close()

	seen, err := store.IsEventSeen(ctx, "conversation:1")
	if err != nil {
		t.Fatalf("IsEventSeen: %v", err)
	}
	if !seen {
		t.Error("expected event to survive reopen")
	}
}

func TestStoreSeenEvents(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	has, err := store.HasSeenEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if has {
		t.Error("fresh store should have no seen events")
	}

	seen, err := store.IsEventSeen(ctx, "thread:5")
	if err != nil {
		t.Fatal(err)
	}
	if seen {
		t.Error("unknown key should not be seen")
	}

	if err := store.MarkEventSeen(ctx, "thread:5", 5); err != nil {
		t.Fatal(err)
	}
	// Marking twice must not error (INSERT OR IGNORE).
	if err := store.MarkEventSeen(ctx, "thread:5", 5); err != nil {
		t.Fatal(err)
	}

	seen, err = store.IsEventSeen(ctx, "thread:5")
	if err != nil {
		t.Fatal(err)
	}
	if !seen {
		t.Error("marked key should be seen")
	}

	has, err = store.HasSeenEvents(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !has {
		t.Error("store should report seen events after marking")
	}
}

func TestStoreAlertStateLifecycle(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	state, err := store.GetAlertState(ctx, 1, models.UnansweredActive)
	if err != nil {
		t.Fatal(err)
	}
	if state != nil {
		t.Error("expected no alert state for unknown ticket")
	}

	// First alert.
	if err := store.RecordAlert(ctx, testAlert(1, models.UnansweredActive, 1)); err != nil {
		t.Fatal(err)
	}
	state, err = store.GetAlertState(ctx, 1, models.UnansweredActive)
	if err != nil {
		t.Fatal(err)
	}
	if state == nil || state.AlertCount != 1 || state.LastAlertAt == nil {
		t.Fatalf("unexpected state after first alert: %+v", state)
	}

	// Reminder increments the count and keeps first_alert_at.
	firstAlertAt := *state.LastAlertAt
	if err := store.RecordAlert(ctx, testAlert(1, models.UnansweredActive, 2)); err != nil {
		t.Fatal(err)
	}
	state, _ = store.GetAlertState(ctx, 1, models.UnansweredActive)
	if state.AlertCount != 2 {
		t.Errorf("AlertCount = %d, want 2", state.AlertCount)
	}
	if !state.LastAlertAt.After(firstAlertAt) && !state.LastAlertAt.Equal(firstAlertAt) {
		t.Error("LastAlertAt should advance on reminder")
	}

	// Different alert type is tracked independently.
	state, err = store.GetAlertState(ctx, 1, models.AgingPending)
	if err != nil {
		t.Fatal(err)
	}
	if state != nil {
		t.Error("aging_pending state should be independent from unanswered_active")
	}

	// Resolve clears tickets that are no longer matching.
	if err := store.RecordAlert(ctx, testAlert(2, models.UnansweredActive, 1)); err != nil {
		t.Fatal(err)
	}
	resolved, err := store.ResolveAlerts(ctx, models.UnansweredActive, []int{2})
	if err != nil {
		t.Fatal(err)
	}
	if resolved != 1 {
		t.Errorf("resolved = %d, want 1", resolved)
	}
	state, _ = store.GetAlertState(ctx, 1, models.UnansweredActive)
	if state != nil {
		t.Error("ticket 1 state should have been resolved")
	}
	state, _ = store.GetAlertState(ctx, 2, models.UnansweredActive)
	if state == nil {
		t.Error("ticket 2 state should still be tracked")
	}

	// Resolving everything works with an empty active list.
	resolved, err = store.ResolveAlerts(ctx, models.UnansweredActive, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resolved != 1 {
		t.Errorf("resolved = %d, want 1", resolved)
	}
}

func TestStoreQueue(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	alert := testAlert(1, models.NewTicket, 1)
	if err := store.Enqueue(ctx, alert, `{"type":"new_ticket"}`); err != nil {
		t.Fatal(err)
	}
	// Enqueueing again for the same ticket+type replaces the payload.
	if err := store.Enqueue(ctx, alert, `{"type":"new_ticket","v":2}`); err != nil {
		t.Fatal(err)
	}

	size, err := store.QueueSize(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if size != 1 {
		t.Errorf("queue size = %d, want 1 (upsert)", size)
	}

	items, err := store.Dequeue(ctx, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 1 {
		t.Fatalf("dequeued %d items, want 1", len(items))
	}
	if items[0].Payload != `{"type":"new_ticket","v":2}` {
		t.Errorf("unexpected payload after upsert: %s", items[0].Payload)
	}
	if items[0].Type != models.NewTicket || items[0].TicketID != 1 {
		t.Errorf("unexpected queue item: %+v", items[0])
	}

	if err := store.RemoveQueued(ctx, items[0].ID); err != nil {
		t.Fatal(err)
	}
	size, _ = store.QueueSize(ctx)
	if size != 0 {
		t.Errorf("queue size after removal = %d, want 0", size)
	}
}

func TestStoreLogAndStats(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	alert := testAlert(1, models.UnansweredActive, 1)
	if err := store.LogNotification(ctx, alert, models.LogSent); err != nil {
		t.Fatal(err)
	}
	if err := store.LogNotification(ctx, testAlert(2, models.NewTicket, 1), models.LogQueued); err != nil {
		t.Fatal(err)
	}
	if err := store.LogNotification(ctx, testAlert(3, models.NewTicket, 1), models.LogFailed); err != nil {
		t.Fatal(err)
	}
	if err := store.RecordAlert(ctx, alert); err != nil {
		t.Fatal(err)
	}

	stats, err := store.Stats(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if stats.TotalSent != 1 {
		t.Errorf("TotalSent = %d, want 1", stats.TotalSent)
	}
	if stats.SentLast24h != 1 {
		t.Errorf("SentLast24h = %d, want 1", stats.SentLast24h)
	}
	if stats.FailedLast24h != 1 {
		t.Errorf("FailedLast24h = %d, want 1", stats.FailedLast24h)
	}
	if stats.ByType[string(models.UnansweredActive)] != 1 {
		t.Errorf("ByType[unanswered_active] = %d, want 1", stats.ByType[string(models.UnansweredActive)])
	}
	if stats.ActiveReminders[string(models.UnansweredActive)] != 1 {
		t.Errorf("ActiveReminders[unanswered_active] = %d, want 1", stats.ActiveReminders[string(models.UnansweredActive)])
	}
}

func TestStoreCleanup(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	if err := store.MarkEventSeen(ctx, "conversation:1", 1); err != nil {
		t.Fatal(err)
	}
	// Backdate the row beyond the retention window.
	if _, err := store.db.ExecContext(ctx,
		`UPDATE seen_events SET seen_at = datetime('now', '-120 days')`); err != nil {
		t.Fatal(err)
	}

	deleted, err := store.Cleanup(ctx, 90)
	if err != nil {
		t.Fatal(err)
	}
	if deleted["seen_events"] != 1 {
		t.Errorf("deleted seen_events = %d, want 1", deleted["seen_events"])
	}

	if err := store.Vacuum(ctx); err != nil {
		t.Errorf("Vacuum: %v", err)
	}
}
