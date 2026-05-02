package main

import (
	"testing"
)

func TestAssignGroup_Deterministic(t *testing.T) {
	user := "user123"
	exp := "exp_v1"

	g1 := AssignGroup(user, exp)
	g2 := AssignGroup(user, exp)

	if g1 != g2 {
		t.Errorf("AssignGroup is not deterministic: %s != %s", g1, g2)
	}
}

func TestAssignGroup_ValidGroups(t *testing.T) {
	validGroups := map[string]bool{"A": true, "B": true, "control": true}

	for i := 0; i < 1000; i++ {
		user := "user"
		exp := "exp"
		g := AssignGroup(user+string(rune(i)), exp)
		if !validGroups[g] {
			t.Errorf("Invalid group returned: %s", g)
		}
	}
}

func TestAssignGroup_DifferentInputs(t *testing.T) {
	g1 := AssignGroup("alice", "exp1")
	g2 := AssignGroup("bob", "exp1")
	g3 := AssignGroup("alice", "exp2")

	_ = g3

	if g1 == g2 {
		t.Logf("alice and bob got same group for same experiment (possible, low probability)")
	}
}

func TestAssignGroup_Distribution(t *testing.T) {
	counts := map[string]int{"A": 0, "B": 0, "control": 0}
	n := 10000
	for i := 0; i < n; i++ {
		g := AssignGroup(string(rune(i)), "distribution_test")
		counts[g]++
	}

	controlRatio := float64(counts["control"]) / float64(n)
	if controlRatio < 0.75 || controlRatio > 0.85 {
		t.Errorf("control ratio out of expected range: got %.2f, want ~0.80", controlRatio)
	}
}
