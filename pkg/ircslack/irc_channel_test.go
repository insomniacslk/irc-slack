package ircslack

import (
	"testing"
)

func TestMembersDiffEmpty(t *testing.T) {
	c := Channel{Members: []string{}}
	a, r := c.MembersDiff([]string{})
	if len(a) != 0 {
		t.Fatalf("Added members: %v; want empty list", a)
	}
	if len(r) != 0 {
		t.Fatalf("Removed members: %v; want empty list", r)
	}
}

func TestMembersDiffNonEmpty(t *testing.T) {
	c := Channel{Members: []string{"removed1"}}
	a, r := c.MembersDiff([]string{"added1"})
	if !(len(a) == 1 && a[0] == "added1") {
		t.Fatalf("Added members: %v; want: %v", a, []string{"added1"})
	}
	if !(len(r) == 1 && r[0] == "removed1") {
		t.Fatalf("Removed members: %v; want: %v", a, []string{"removed1"})
	}
}
