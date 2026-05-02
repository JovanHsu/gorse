// Copyright 2025 gorse Project Authors
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
// http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// Package config defines configuration schemas and structured types.
//
// Labels Schema (Phase 4+)
//
// Gorse stores arbitrary JSON in User.Labels and Item.Labels (typed as any).
// This file defines canonical, type-safe Go structs for all Labels fields
// used across the Cadoo dating recommendation system.
//
// Design principles:
//   - All numeric fields use float64 unless int is explicitly required
//     (JSON unmarshal defaults to float64 for numbers)
//   - Schema is additive-only: new fields can be added without breaking existing data
//   - Field presence is explicit via HasXxx() methods for optional fields
//   - Gender lives in both Labels and User.Gender/Item.Categories — Labels copy is for convenience
//
// Upgrade guide: When adding a new field, add it to the struct with a json tag,
// add a HasXxx() method, and update ToMap() and Validate() accordingly.

package config

import (
	"slices"
	"time"
)

// UserLabels holds all structured fields for User.Labels.
// These fields are used by profile_match, behavior tracking, VIP detection, and lifecycle classification.
type UserLabels struct {
	// Profile identification
	Age               int     `json:"age,omitempty"`                // User's age in years
	City              string  `json:"city,omitempty"`               // User's city (e.g., "beijing")
	Purpose           string  `json:"purpose,omitempty"`            // Dating purpose: "relationship", "friendship", "casual"
	PhotoCount        int     `json:"photo_count,omitempty"`        // Number of profile photos uploaded
	QualityScore      float64 `json:"quality_score,omitempty"`      // Overall profile quality 0.0-1.0
	IsVerified        bool    `json:"is_verified,omitempty"`       // Identity verification status (stored as int 0/1 in DB)
	Gender            string  `json:"gender,omitempty"`              // User's gender: "M", "F", "O" (duplicated from User.Gender for convenience)
	Tier              string  `json:"tier,omitempty"`               // User tier: "vip", "premium", "standard"
	SignupTime        string  `json:"signup_time,omitempty"`        // RFC3339 timestamp of account creation
	IsNewUser         bool    `json:"is_new_user,omitempty"`       // In new-user period (< 7 days since signup)
	ProfileCompleteness float64 `json:"profile_completeness,omitempty"` // Profile completion ratio 0.0-1.0

	// Partner preferences (used by profile_match recommender)
	PrefAgeMin          int    `json:"pref_age_min,omitempty"`           // Minimum preferred partner age
	PrefAgeMax          int    `json:"pref_age_max,omitempty"`           // Maximum preferred partner age
	PrefCity            string `json:"pref_city,omitempty"`               // Preferred partner city
	PrefPurpose         string `json:"pref_purpose,omitempty"`            // Preferred partner dating purpose
	PrefMaxDistanceKm   int    `json:"pref_max_distance_km,omitempty"`   // Maximum distance to partner in km
	Latitude            float64 `json:"latitude,omitempty"`              // User's latitude (-90 to 90)
	Longitude           float64 `json:"longitude,omitempty"`             // User's longitude (-180 to 180)

	// Behavior statistics (written by BehaviorStatsComputer, read by behavior tracker)
	RightSwipeRate      float64 `json:"right_swipe_rate,omitempty"`      // Ratio of (likes+matches) to total swipes 0.0-1.0
	AvgSwipeDurationMs  int     `json:"avg_swipe_duration_ms,omitempty"` // Average swipe decision time in milliseconds
	BehaviorStability   float64 `json:"behavior_stability,omitempty"`    // Swipe consistency score 0.0-1.0 (1 = very stable)
	ExplorationRatio    float64 `json:"exploration_ratio,omitempty"`     // Effective exploration ratio 0.0-1.0
	RightSwipeCount     int     `json:"right_swipe_count,omitempty"`    // Total count of likes+matches
	TotalSwipeCount     int     `json:"total_swipe_count,omitempty"`    // Total count of all swipes
	MatchRate           float64 `json:"match_rate,omitempty"`           // Ratio of matches to total swipes 0.0-1.0
	StatsUpdatedAt      string  `json:"stats_updated_at,omitempty"`      // RFC3339 timestamp of last behavior stats update
	LastActive          string  `json:"last_active,omitempty"`          // RFC3339 timestamp of last user activity

	// Experiment assignment
	ABGroup string `json:"ab_group,omitempty"` // A/B experiment group (e.g., "B", "control")
}

// ItemLabels holds all structured fields for Item.Labels.
// In the Cadoo dating app, Items are user profiles, so these fields mirror UserLabels
// for profile matching, quality scoring, and diversity control.
type ItemLabels struct {
	// Profile identification
	Age          int     `json:"age,omitempty"`           // Displayed age in years
	City         string  `json:"city,omitempty"`           // User's city
	Purpose      string  `json:"purpose,omitempty"`        // Dating purpose: "relationship", "friendship", "casual"
	PhotoCount   int     `json:"photo_count,omitempty"`   // Number of photos
	QualityScore float64 `json:"quality_score,omitempty"` // Profile quality score 0.0-1.0
	IsVerified   bool    `json:"is_verified,omitempty"`   // Identity verification status (stored as int 0/1 in DB)
	Style        string  `json:"style,omitempty"`        // Profile style: "elegant", "casual", "sporty", etc.

	// Quality and engagement metrics (computed by SuccessRateComputer)
	BeautyScore float64 `json:"beauty_score,omitempty"` // AI-estimated attractiveness score 0.0-1.0
	LikeRate   float64 `json:"like_rate,omitempty"`    // Historical ratio of likes to total impressions 0.0-1.0
	ReplyRate  float64 `json:"reply_rate,omitempty"`  // Historical ratio of replies to matches 0.0-1.0
	ReportRate float64 `json:"report_rate,omitempty"`  // Historical ratio of reports to impressions 0.0-1.0
	BlockRate  float64 `json:"block_rate,omitempty"`   // Historical ratio of blocks to impressions 0.0-1.0

	// Quality flags
	IsHighQuality bool    `json:"is_high_quality,omitempty"` // Manual quality flag from moderators
	RiskScore     float64 `json:"risk_score,omitempty"`      // Risk/fraud score 0.0-1.0 (higher = riskier)

	// CVR prediction (written by external service or model)
	CVRScore float64 `json:"cvr_score,omitempty"` // Predicted conversion rate (match → chat) 0.0-1.0

	// Activity patterns
	ActiveHours []int `json:"active_hours,omitempty"` // Hours of day when user is most active (0-23), e.g. [22,23,0,1,2]

	// Geolocation (used by profile_match for distance filtering)
	Latitude  float64 `json:"latitude,omitempty"`  // Geographic latitude (-90 to 90)
	Longitude float64 `json:"longitude,omitempty"`   // Geographic longitude (-180 to 180)

	// Target demographics (written by user preference, read by profile_match)
	GenderTarget []string `json:"gender_target,omitempty"` // Target genders for this profile: ["M"], ["F"], ["M","F"]
}

// getFloat64 safely extracts a float64 from a map value.
func getFloat64(m map[string]any, key string) float64 {
	if v, ok := m[key].(float64); ok {
		return v
	}
	return 0
}

// getInt safely extracts an int from a map value (JSON numbers are float64).
func getInt(m map[string]any, key string) int {
	return int(getFloat64(m, key))
}

// getBool safely extracts a bool from a map value (Gorse stores bools as int 0/1 in DB).
func getBool(m map[string]any, key string) bool {
	if v, ok := m[key].(float64); ok {
		return v == 1
	}
	if v, ok := m[key].(bool); ok {
		return v
	}
	return false
}

// getString safely extracts a string from a map value.
func getString(m map[string]any, key string) string {
	if v, ok := m[key].(string); ok {
		return v
	}
	return ""
}

// getIntSlice safely extracts an []int from a map value (JSON numbers are float64).
func getIntSlice(m map[string]any, key string) []int {
	raw, ok := m[key].([]any)
	if !ok {
		return nil
	}
	result := make([]int, 0, len(raw))
	for _, v := range raw {
		if f, ok := v.(float64); ok {
			result = append(result, int(f))
		}
	}
	return result
}

// getStringSlice safely extracts a []string from a map value.
func getStringSlice(m map[string]any, key string) []string {
	raw, ok := m[key].([]any)
	if !ok {
		return nil
	}
	result := make([]string, 0, len(raw))
	for _, v := range raw {
		if s, ok := v.(string); ok {
			result = append(result, s)
		}
	}
	return result
}

// ToUserLabels converts a raw map[string]any (from User.Labels) to a typed UserLabels struct.
// Returns false if the input is nil. This handles the fact that Gorse stores numbers as
// float64 in JSON and bools as int 0/1 in the database.
func ToUserLabels(labels map[string]any) (UserLabels, bool) {
	if labels == nil {
		return UserLabels{}, false
	}
	return UserLabels{
		Age:                 getInt(labels, "age"),
		City:                getString(labels, "city"),
		Purpose:             getString(labels, "purpose"),
		PhotoCount:          getInt(labels, "photo_count"),
		QualityScore:        getFloat64(labels, "quality_score"),
		IsVerified:          getBool(labels, "is_verified"),
		Gender:              getString(labels, "gender"),
		Tier:                getString(labels, "tier"),
		SignupTime:          getString(labels, "signup_time"),
		IsNewUser:           getBool(labels, "is_new_user"),
		ProfileCompleteness:  getFloat64(labels, "profile_completeness"),
		PrefAgeMin:          getInt(labels, "pref_age_min"),
		PrefAgeMax:          getInt(labels, "pref_age_max"),
		PrefCity:            getString(labels, "pref_city"),
		PrefPurpose:         getString(labels, "pref_purpose"),
		PrefMaxDistanceKm:   getInt(labels, "pref_max_distance_km"),
		Latitude:            getFloat64(labels, "latitude"),
		Longitude:           getFloat64(labels, "longitude"),
		RightSwipeRate:      getFloat64(labels, "right_swipe_rate"),
		AvgSwipeDurationMs:  getInt(labels, "avg_swipe_duration_ms"),
		BehaviorStability:   getFloat64(labels, "behavior_stability"),
		ExplorationRatio:     getFloat64(labels, "exploration_ratio"),
		RightSwipeCount:    getInt(labels, "right_swipe_count"),
		TotalSwipeCount:    getInt(labels, "total_swipe_count"),
		MatchRate:           getFloat64(labels, "match_rate"),
		StatsUpdatedAt:      getString(labels, "stats_updated_at"),
		LastActive:          getString(labels, "last_active"),
		ABGroup:             getString(labels, "ab_group"),
	}, true
}

// ToItemLabels converts a raw map[string]any (from Item.Labels) to a typed ItemLabels struct.
// Returns false if the input is nil.
func ToItemLabels(labels map[string]any) (ItemLabels, bool) {
	if labels == nil {
		return ItemLabels{}, false
	}
	return ItemLabels{
		Age:          getInt(labels, "age"),
		City:         getString(labels, "city"),
		Purpose:      getString(labels, "purpose"),
		PhotoCount:   getInt(labels, "photo_count"),
		QualityScore: getFloat64(labels, "quality_score"),
		IsVerified:   getBool(labels, "is_verified"),
		Style:        getString(labels, "style"),
		BeautyScore: getFloat64(labels, "beauty_score"),
		LikeRate:     getFloat64(labels, "like_rate"),
		ReplyRate:    getFloat64(labels, "reply_rate"),
		ReportRate:   getFloat64(labels, "report_rate"),
		BlockRate:    getFloat64(labels, "block_rate"),
		IsHighQuality: getBool(labels, "is_high_quality"),
		RiskScore:    getFloat64(labels, "risk_score"),
		CVRScore:     getFloat64(labels, "cvr_score"),
		ActiveHours:  getIntSlice(labels, "active_hours"),
		GenderTarget: getStringSlice(labels, "gender_target"),
		Latitude:     getFloat64(labels, "latitude"),
		Longitude:    getFloat64(labels, "longitude"),
	}, true
}

// ToMap serializes a UserLabels struct to map[string]any for storage in User.Labels.
// Zero-value fields are omitted.
func (u UserLabels) ToMap() map[string]any {
	m := make(map[string]any)
	if u.Age != 0                       { m["age"] = u.Age }
	if u.City != ""                     { m["city"] = u.City }
	if u.Purpose != ""                   { m["purpose"] = u.Purpose }
	if u.PhotoCount != 0                { m["photo_count"] = u.PhotoCount }
	if u.QualityScore != 0              { m["quality_score"] = u.QualityScore }
	if u.IsVerified                      { m["is_verified"] = u.IsVerified }
	if u.Gender != ""                    { m["gender"] = u.Gender }
	if u.Tier != ""                      { m["tier"] = u.Tier }
	if u.SignupTime != ""                { m["signup_time"] = u.SignupTime }
	if u.IsNewUser                       { m["is_new_user"] = u.IsNewUser }
	if u.ProfileCompleteness != 0        { m["profile_completeness"] = u.ProfileCompleteness }
	if u.PrefAgeMin != 0                { m["pref_age_min"] = u.PrefAgeMin }
	if u.PrefAgeMax != 0                { m["pref_age_max"] = u.PrefAgeMax }
	if u.PrefCity != ""                  { m["pref_city"] = u.PrefCity }
	if u.PrefPurpose != ""               { m["pref_purpose"] = u.PrefPurpose }
	if u.PrefMaxDistanceKm != 0         { m["pref_max_distance_km"] = u.PrefMaxDistanceKm }
	if u.Latitude != 0                 { m["latitude"] = u.Latitude }
	if u.Longitude != 0                { m["longitude"] = u.Longitude }
	if u.RightSwipeRate != 0            { m["right_swipe_rate"] = u.RightSwipeRate }
	if u.AvgSwipeDurationMs != 0        { m["avg_swipe_duration_ms"] = u.AvgSwipeDurationMs }
	if u.BehaviorStability != 0         { m["behavior_stability"] = u.BehaviorStability }
	if u.ExplorationRatio != 0          { m["exploration_ratio"] = u.ExplorationRatio }
	if u.RightSwipeCount != 0           { m["right_swipe_count"] = u.RightSwipeCount }
	if u.TotalSwipeCount != 0           { m["total_swipe_count"] = u.TotalSwipeCount }
	if u.MatchRate != 0                  { m["match_rate"] = u.MatchRate }
	if u.StatsUpdatedAt != ""            { m["stats_updated_at"] = u.StatsUpdatedAt }
	if u.LastActive != ""                { m["last_active"] = u.LastActive }
	if u.ABGroup != ""                  { m["ab_group"] = u.ABGroup }
	return m
}

// ToMap serializes an ItemLabels struct to map[string]any for storage in Item.Labels.
func (i ItemLabels) ToMap() map[string]any {
	m := make(map[string]any)
	if i.Age != 0           { m["age"] = i.Age }
	if i.City != ""         { m["city"] = i.City }
	if i.Purpose != ""      { m["purpose"] = i.Purpose }
	if i.PhotoCount != 0    { m["photo_count"] = i.PhotoCount }
	if i.QualityScore != 0  { m["quality_score"] = i.QualityScore }
	if i.IsVerified         { m["is_verified"] = i.IsVerified }
	if i.Style != ""        { m["style"] = i.Style }
	if i.BeautyScore != 0   { m["beauty_score"] = i.BeautyScore }
	if i.LikeRate != 0      { m["like_rate"] = i.LikeRate }
	if i.ReplyRate != 0     { m["reply_rate"] = i.ReplyRate }
	if i.ReportRate != 0    { m["report_rate"] = i.ReportRate }
	if i.BlockRate != 0     { m["block_rate"] = i.BlockRate }
	if i.IsHighQuality      { m["is_high_quality"] = i.IsHighQuality }
	if i.RiskScore != 0     { m["risk_score"] = i.RiskScore }
	if i.CVRScore != 0      { m["cvr_score"] = i.CVRScore }
	if len(i.ActiveHours) > 0 { m["active_hours"] = i.ActiveHours }
	if len(i.GenderTarget) > 0 { m["gender_target"] = i.GenderTarget }
	if i.Latitude != 0         { m["latitude"] = i.Latitude }
	if i.Longitude != 0        { m["longitude"] = i.Longitude }
	return m
}

// Validate checks that UserLabels fields are within acceptable ranges.
func (u UserLabels) Validate() error {
	if u.Age < 0 || u.Age > 120 {
		return &LabelValidationError{Field: "age", Value: u.Age, Reason: "must be between 0 and 120"}
	}
	if u.PrefAgeMin < 0 || u.PrefAgeMin > 120 {
		return &LabelValidationError{Field: "pref_age_min", Value: u.PrefAgeMin, Reason: "must be between 0 and 120"}
	}
	if u.PrefAgeMax < 0 || u.PrefAgeMax > 120 {
		return &LabelValidationError{Field: "pref_age_max", Value: u.PrefAgeMax, Reason: "must be between 0 and 120"}
	}
	if u.PrefAgeMin > u.PrefAgeMax && u.PrefAgeMin != 0 {
		return &LabelValidationError{Field: "pref_age_range", Reason: "pref_age_min cannot exceed pref_age_max"}
	}
	if u.RightSwipeRate < 0 || u.RightSwipeRate > 1 {
		return &LabelValidationError{Field: "right_swipe_rate", Value: u.RightSwipeRate, Reason: "must be between 0.0 and 1.0"}
	}
	if u.BehaviorStability < 0 || u.BehaviorStability > 1 {
		return &LabelValidationError{Field: "behavior_stability", Value: u.BehaviorStability, Reason: "must be between 0.0 and 1.0"}
	}
	if u.ExplorationRatio < 0 || u.ExplorationRatio > 1 {
		return &LabelValidationError{Field: "exploration_ratio", Value: u.ExplorationRatio, Reason: "must be between 0.0 and 1.0"}
	}
	if u.MatchRate < 0 || u.MatchRate > 1 {
		return &LabelValidationError{Field: "match_rate", Value: u.MatchRate, Reason: "must be between 0.0 and 1.0"}
	}
	if u.QualityScore < 0 || u.QualityScore > 1 {
		return &LabelValidationError{Field: "quality_score", Value: u.QualityScore, Reason: "must be between 0.0 and 1.0"}
	}
	if u.Gender != "" && !slices.Contains([]string{"M", "F", "O", "male", "female", "other"}, u.Gender) {
		return &LabelValidationError{Field: "gender", Value: u.Gender, Reason: "must be one of M, F, O, male, female, other"}
	}
	if u.Purpose != "" && !slices.Contains([]string{"relationship", "friendship", "casual", "networking"}, u.Purpose) {
		return &LabelValidationError{Field: "purpose", Value: u.Purpose, Reason: "must be one of relationship, friendship, casual, networking"}
	}
	if u.StatsUpdatedAt != "" {
		if _, err := time.Parse(time.RFC3339, u.StatsUpdatedAt); err != nil {
			return &LabelValidationError{Field: "stats_updated_at", Value: u.StatsUpdatedAt, Reason: "must be RFC3339 format"}
		}
	}
	if u.Latitude != 0 && (u.Latitude < -90 || u.Latitude > 90) {
		return &LabelValidationError{Field: "latitude", Value: u.Latitude, Reason: "must be between -90 and 90"}
	}
	if u.Longitude != 0 && (u.Longitude < -180 || u.Longitude > 180) {
		return &LabelValidationError{Field: "longitude", Value: u.Longitude, Reason: "must be between -180 and 180"}
	}
	return nil
}

// Validate checks that ItemLabels fields are within acceptable ranges.
func (i ItemLabels) Validate() error {
	if i.Age < 0 || i.Age > 120 {
		return &LabelValidationError{Field: "age", Value: i.Age, Reason: "must be between 0 and 120"}
	}
	if i.QualityScore < 0 || i.QualityScore > 1 {
		return &LabelValidationError{Field: "quality_score", Value: i.QualityScore, Reason: "must be between 0.0 and 1.0"}
	}
	if i.LikeRate < 0 || i.LikeRate > 1 {
		return &LabelValidationError{Field: "like_rate", Value: i.LikeRate, Reason: "must be between 0.0 and 1.0"}
	}
	if i.ReplyRate < 0 || i.ReplyRate > 1 {
		return &LabelValidationError{Field: "reply_rate", Value: i.ReplyRate, Reason: "must be between 0.0 and 1.0"}
	}
	if i.ReportRate < 0 || i.ReportRate > 1 {
		return &LabelValidationError{Field: "report_rate", Value: i.ReportRate, Reason: "must be between 0.0 and 1.0"}
	}
	if i.BlockRate < 0 || i.BlockRate > 1 {
		return &LabelValidationError{Field: "block_rate", Value: i.BlockRate, Reason: "must be between 0.0 and 1.0"}
	}
	if i.BeautyScore < 0 || i.BeautyScore > 1 {
		return &LabelValidationError{Field: "beauty_score", Value: i.BeautyScore, Reason: "must be between 0.0 and 1.0"}
	}
	if i.RiskScore < 0 || i.RiskScore > 1 {
		return &LabelValidationError{Field: "risk_score", Value: i.RiskScore, Reason: "must be between 0.0 and 1.0"}
	}
	if i.CVRScore < 0 || i.CVRScore > 1 {
		return &LabelValidationError{Field: "cvr_score", Value: i.CVRScore, Reason: "must be between 0.0 and 1.0"}
	}
	for _, h := range i.ActiveHours {
		if h < 0 || h > 23 {
			return &LabelValidationError{Field: "active_hours", Value: h, Reason: "each hour must be between 0 and 23"}
		}
	}
	if i.Latitude != 0 && (i.Latitude < -90 || i.Latitude > 90) {
		return &LabelValidationError{Field: "latitude", Value: i.Latitude, Reason: "must be between -90 and 90"}
	}
	if i.Longitude != 0 && (i.Longitude < -180 || i.Longitude > 180) {
		return &LabelValidationError{Field: "longitude", Value: i.Longitude, Reason: "must be between -180 and 180"}
	}
	return nil
}

// LabelValidationError represents a validation failure for a Labels field.
type LabelValidationError struct {
	Field  string
	Value  any
	Reason string
}

func (e *LabelValidationError) Error() string {
	return "invalid " + e.Field + ": " + e.Reason
}
