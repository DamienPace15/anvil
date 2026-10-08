package complianceScanner

import (
	"fmt"
	"hash/fnv"
	"regexp"
	"strings"
)

var retentionTiers = map[string]bool{"30d": true, "90d": true, "180d": true, "1y": true, "2y": true, "7y": true}

var (
	regionRe = regexp.MustCompile(`^[a-z]{2}(-[a-z]+)+-[0-9]$`)
	// Six AWS cron fields: minutes hours day-of-month month day-of-week year.
	cronFieldRe = regexp.MustCompile(`^[0-9A-Z*?,/#L W-]+$`)
	timezoneRe  = regexp.MustCompile(`^[A-Za-z_]+(/[A-Za-z0-9_+-]+)*$`)
	nameCharRe  = regexp.MustCompile(`[^a-zA-Z0-9_.-]`)
)

const (
	scheduleDaily  = "daily"
	scheduleWeekly = "weekly"
	scheduleNone   = "none"
	scheduleCron   = "cron"
)

// config is the validated, resolved component configuration.
type config struct {
	Frameworks         []string // Prowler IDs
	Schedule           string   // daily | weekly | none | cron
	ScheduleExpression string   // "" when Schedule is none
	Timezone           string
	Retention          string
	ScanOnDeploy       bool
	Regions            []string
}

// resolve validates args and fills defaults. defaultRegion is the region of the
// provider the component runs under; it is always scanned.
func resolve(args ComplianceScannerArgs, project, stage, defaultRegion string) (config, error) {
	cfg := config{ScanOnDeploy: args.ScanOnDeploy}

	if len(args.Frameworks) == 0 {
		return cfg, fmt.Errorf("compliance.frameworks is required: choose at least one of %s, or a Prowler AWS compliance ID", strings.Join(friendlyNames(), ", "))
	}
	seen := map[string]bool{}
	for _, name := range args.Frameworks {
		id, err := resolveFramework(name)
		if err != nil {
			return cfg, err
		}
		if !seen[id] {
			seen[id] = true
			cfg.Frameworks = append(cfg.Frameworks, id)
		}
	}

	cfg.Retention = args.Retention
	if cfg.Retention == "" {
		cfg.Retention = "1y"
	}
	if !retentionTiers[cfg.Retention] {
		return cfg, fmt.Errorf("compliance.retention must be one of 30d, 90d, 180d, 1y, 2y, 7y, got %q", cfg.Retention)
	}

	cfg.Timezone = args.Timezone
	if cfg.Timezone == "" {
		cfg.Timezone = "UTC"
	}
	if !timezoneRe.MatchString(cfg.Timezone) {
		return cfg, fmt.Errorf("compliance schedule timezone %q is not a valid IANA name (e.g. Australia/Sydney)", cfg.Timezone)
	}

	cfg.Schedule = args.Schedule
	if cfg.Schedule == "" {
		cfg.Schedule = scheduleDaily
	}
	minute, hour, weekday := startTime(project, stage)
	switch cfg.Schedule {
	case scheduleDaily:
		cfg.ScheduleExpression = fmt.Sprintf("cron(%d %d * * ? *)", minute, hour)
	case scheduleWeekly:
		cfg.ScheduleExpression = fmt.Sprintf("cron(%d %d ? * %s *)", minute, hour, weekday)
	case scheduleNone:
	case scheduleCron:
		expr, err := cronExpression(args.Cron)
		if err != nil {
			return cfg, err
		}
		cfg.ScheduleExpression = expr
	default:
		return cfg, fmt.Errorf("compliance.schedule must be daily, weekly, none or { cron, timezone }, got %q", cfg.Schedule)
	}

	regionSeen := map[string]bool{}
	for _, r := range append([]string{defaultRegion}, args.Regions...) {
		if r == "" || regionSeen[r] {
			continue
		}
		if !regionRe.MatchString(r) {
			return cfg, fmt.Errorf("invalid region %q", r)
		}
		regionSeen[r] = true
		cfg.Regions = append(cfg.Regions, r)
	}
	if len(cfg.Regions) == 0 {
		return cfg, fmt.Errorf("no AWS region configured for compliance scanning")
	}

	return cfg, nil
}

// startTime spreads presets across the day/week so many apps in one account
// don't all scan at once. It is derived from project+stage, so it's stable
// across deploys.
func startTime(project, stage string) (minute, hour int, weekday string) {
	h := fnv.New32a()
	h.Write([]byte(project + "/" + stage))
	v := int(h.Sum32())
	days := []string{"SUN", "MON", "TUE", "WED", "THU", "FRI", "SAT"}
	return v % 60, (v / 60) % 24, days[(v/1440)%7]
}

// cronExpression accepts the six AWS cron fields, with or without the cron()
// wrapper.
func cronExpression(raw string) (string, error) {
	expr := strings.TrimSpace(raw)
	if strings.HasPrefix(expr, "cron(") && strings.HasSuffix(expr, ")") {
		expr = strings.TrimSuffix(strings.TrimPrefix(expr, "cron("), ")")
	}
	fields := strings.Fields(expr)
	if len(fields) != 6 {
		return "", fmt.Errorf("compliance schedule cron %q must have 6 fields (minutes hours day-of-month month day-of-week year), e.g. \"0 3 * * ? *\"", raw)
	}
	for _, f := range fields {
		if !cronFieldRe.MatchString(f) {
			return "", fmt.Errorf("compliance schedule cron %q has an invalid field %q", raw, f)
		}
	}
	// A wildcard minute would scan every minute.
	if fields[0] == "*" || strings.Contains(fields[0], "/") || strings.Contains(fields[0], ",") || strings.Contains(fields[0], "-") {
		return "", fmt.Errorf("compliance schedule cron %q runs more than once an hour; use a single minute value", raw)
	}
	return "cron(" + strings.Join(fields, " ") + ")", nil
}

// scheduleName is unique per app in the shared schedule group. EventBridge
// Scheduler allows [0-9a-zA-Z-_.] up to 64 characters.
func scheduleName(project, stage string) string {
	name := "anvil-" + nameCharRe.ReplaceAllString(project, "-") + "-" + nameCharRe.ReplaceAllString(stage, "-")
	if len(name) <= 64 {
		return name
	}
	h := fnv.New32a()
	h.Write([]byte(project + "/" + stage))
	suffix := fmt.Sprintf("-%08x", h.Sum32())
	return name[:64-len(suffix)] + suffix
}
