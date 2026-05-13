package gitrepo

import (
	"context"
	"fmt"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type Commit struct {
	Hash      string
	ShortHash string
	Author    string
	Email     string
	Date      time.Time
	Subject   string
	IssueIDs  []int64
}

var issueRefPattern = regexp.MustCompile(`(?i)(?:#|PME-)([0-9]+)`)

func Scan(ctx context.Context, path string, limit int) ([]Commit, error) {
	if strings.TrimSpace(path) == "" {
		return nil, fmt.Errorf("repository path is required")
	}
	if limit < 1 || limit > 1000 {
		limit = 200
	}

	args := []string{
		"-C", path,
		"log",
		"--date=iso-strict",
		"--pretty=format:%H%x1f%h%x1f%an%x1f%ae%x1f%aI%x1f%s%x1f%b%x1e",
		"-n", strconv.Itoa(limit),
	}
	output, err := exec.CommandContext(ctx, "git", args...).Output()
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			return nil, fmt.Errorf("git log failed: %s", strings.TrimSpace(string(exitErr.Stderr)))
		}
		return nil, err
	}

	records := strings.Split(string(output), "\x1e")
	commits := make([]Commit, 0, len(records))
	for _, record := range records {
		record = strings.TrimSpace(record)
		if record == "" {
			continue
		}
		fields := strings.SplitN(record, "\x1f", 7)
		if len(fields) < 7 {
			continue
		}
		issueIDs := extractIssueIDs(fields[5] + "\n" + fields[6])
		if len(issueIDs) == 0 {
			continue
		}
		date, _ := time.Parse(time.RFC3339, strings.TrimSpace(fields[4]))
		commits = append(commits, Commit{
			Hash:      strings.TrimSpace(fields[0]),
			ShortHash: strings.TrimSpace(fields[1]),
			Author:    strings.TrimSpace(fields[2]),
			Email:     strings.TrimSpace(fields[3]),
			Date:      date,
			Subject:   strings.TrimSpace(fields[5]),
			IssueIDs:  issueIDs,
		})
	}
	return commits, nil
}

func extractIssueIDs(text string) []int64 {
	matches := issueRefPattern.FindAllStringSubmatch(text, -1)
	seen := make(map[int64]struct{}, len(matches))
	ids := make([]int64, 0, len(matches))
	for _, match := range matches {
		if len(match) != 2 {
			continue
		}
		id, err := strconv.ParseInt(match[1], 10, 64)
		if err != nil || id < 1 {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		ids = append(ids, id)
	}
	return ids
}
