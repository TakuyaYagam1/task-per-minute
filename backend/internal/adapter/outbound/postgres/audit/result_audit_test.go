package audit

import (
	"testing"
	"time"

	"github.com/google/uuid"
)

func TestValidAuditTimeRange(t *testing.T) {
	t.Parallel()

	from := time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC)
	to := from.Add(time.Minute)
	nonUTC := from.In(time.FixedZone("non-utc", 60))

	tests := []struct {
		name string
		from *time.Time
		to   *time.Time
		want bool
	}{
		{name: "unbounded", want: true},
		{name: "bounded", from: &from, to: &to, want: true},
		{name: "reversed", from: &to, to: &from, want: false},
		{name: "non UTC from", from: &nonUTC, want: false},
		{name: "non UTC to", to: &nonUTC, want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := validAuditTimeRange(test.from, test.to); got != test.want {
				t.Fatalf("validAuditTimeRange() = %t, want %t", got, test.want)
			}
		})
	}
}

func TestValidAuditCursor(t *testing.T) {
	t.Parallel()

	valid := AuditCursor{
		OccurredAt:    time.Date(2026, time.January, 2, 3, 4, 5, 0, time.UTC),
		AuditEventID:  uuid.New(),
		RevisionID:    uuid.New(),
		SnapshotBound: "42:47:42,45",
	}
	invalidID := valid
	invalidID.AuditEventID = uuid.Nil
	invalidSnapshot := valid
	invalidSnapshot.SnapshotBound = "42:47"

	if !validAuditCursor(nil) {
		t.Fatal("nil cursor must be valid")
	}
	if !validAuditCursor(&valid) {
		t.Fatal("complete cursor must be valid")
	}
	if validAuditCursor(&invalidID) {
		t.Fatal("cursor without audit event ID must be rejected")
	}
	if validAuditCursor(&invalidSnapshot) {
		t.Fatal("cursor with malformed snapshot must be rejected")
	}
}

func TestValidAuditSnapshot(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name  string
		value string
		want  bool
	}{
		{name: "empty active set", value: "42:42:", want: true},
		{name: "active transactions", value: "42:47:42,45", want: true},
		{name: "missing fields", value: "42:47", want: false},
		{name: "zero bound", value: "0:47:", want: false},
		{name: "non numeric bound", value: "before:47:", want: false},
		{name: "reversed bounds", value: "47:42:", want: false},
		{name: "active xid below xmin", value: "42:47:41", want: false},
		{name: "active xid at xmax", value: "42:47:47", want: false},
		{name: "active xids out of order", value: "42:47:45,42", want: false},
		{name: "duplicate active xid", value: "42:47:42,42", want: false},
		{name: "empty active xid", value: "42:47:42,", want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			if got := validAuditSnapshot(test.value); got != test.want {
				t.Fatalf("validAuditSnapshot(%q) = %t, want %t", test.value, got, test.want)
			}
		})
	}
}
