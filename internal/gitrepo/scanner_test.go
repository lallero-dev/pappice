package gitrepo

import "testing"

func TestExtractIssueRefs(t *testing.T) {
	refs := extractIssueRefs("fix #12 and PME-34\n\nRefs pme-34 and OPS-7 and #12")
	if len(refs) != 3 {
		t.Fatalf("refs = %#v, want 3 unique refs", refs)
	}
	assertRef(t, refs[0], "", 12)
	assertRef(t, refs[1], "PME", 34)
	assertRef(t, refs[2], "OPS", 7)

	ids := issueIDs(refs)
	if len(ids) != 3 || ids[0] != 12 || ids[1] != 34 || ids[2] != 7 {
		t.Fatalf("ids = %#v", ids)
	}
}

func assertRef(t *testing.T, ref IssueRef, key string, number int64) {
	t.Helper()
	if ref.ProjectKey != key || ref.Number != number {
		t.Fatalf("ref = %#v, want %s-%d", ref, key, number)
	}
}
