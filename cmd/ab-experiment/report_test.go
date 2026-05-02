package main

import (
	"testing"
)

func TestReportResponseStructure(t *testing.T) {
	report := &ReportResponse{
		Experiment: "test_exp",
		Groups: map[string]GroupMetrics{
			"control": {MatchRate: 0.30, SampleSize: 1000},
			"A":       {MatchRate: 0.38, Lift: "+26.7%"},
			"B":       {MatchRate: 0.35, Lift: "+16.7%"},
		},
	}

	if report.Experiment != "test_exp" {
		t.Errorf("expected experiment test_exp, got %s", report.Experiment)
	}

	if len(report.Groups) != 3 {
		t.Errorf("expected 3 groups, got %d", len(report.Groups))
	}

	if report.Groups["control"].MatchRate != 0.30 {
		t.Errorf("expected control match rate 0.30, got %f", report.Groups["control"].MatchRate)
	}

	if report.Groups["A"].Lift != "+26.7%" {
		t.Errorf("expected A lift +26.7%%, got %s", report.Groups["A"].Lift)
	}
}

func TestLiftCalculation(t *testing.T) {
	controlRate := 0.30
	groupRate := 0.38

	lift := ((groupRate - controlRate) / controlRate) * 100

	if lift < 26.6 || lift > 26.8 {
		t.Errorf("expected lift ~26.7%%, got %.2f%%", lift)
	}
}

func TestGroupMetrics_Empty(t *testing.T) {
	gm := GroupMetrics{}

	if gm.MatchRate != 0 {
		t.Errorf("expected zero match rate, got %f", gm.MatchRate)
	}

	if gm.SampleSize != 0 {
		t.Errorf("expected zero sample size, got %d", gm.SampleSize)
	}
}
