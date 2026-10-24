package services

import (
	"cmp"
	"slices"
	"time"

	"github.com/muety/wakapi/config"
	"github.com/muety/wakapi/models"
)

// BranchActivity is the time a user spent on one branch of one project on one local day.
type BranchActivity struct {
	Project string
	Date    string // YYYY-MM-DD in the requested time zone
	Branch  string
	Seconds float64
	FirstAt time.Time
	LastAt  time.Time
}

// AggregateBranchActivity sums durations per project, local day and branch. Durations are clipped
// to [from, to] and split at local midnight, so a day's total over all branches equals the
// project's coding time that day. Durations are one non-overlapping timeline per user, so time on
// parallel branches is never counted twice.
func AggregateBranchActivity(durations models.Durations, from, to time.Time, tz *time.Location) []*BranchActivity {
	type key struct{ project, date, branch string }
	byKey := map[key]*BranchActivity{}

	for _, d := range durations {
		start, end := d.Time.T(), d.TimeEnd()
		if start.Before(from) {
			start = from
		}
		if end.After(to) {
			end = to
		}
		for start.Before(end) {
			local := start.In(tz)
			nextMidnight := time.Date(local.Year(), local.Month(), local.Day()+1, 0, 0, 0, 0, tz)
			sliceEnd := end
			if nextMidnight.Before(sliceEnd) {
				sliceEnd = nextMidnight
			}
			k := key{d.Project, local.Format(config.SimpleDateFormat), d.Branch}
			item, ok := byKey[k]
			if !ok {
				item = &BranchActivity{Project: k.project, Date: k.date, Branch: k.branch, FirstAt: start, LastAt: sliceEnd}
				byKey[k] = item
			}
			item.Seconds += sliceEnd.Sub(start).Seconds()
			if start.Before(item.FirstAt) {
				item.FirstAt = start
			}
			if sliceEnd.After(item.LastAt) {
				item.LastAt = sliceEnd
			}
			start = sliceEnd
		}
	}

	result := make([]*BranchActivity, 0, len(byKey))
	for _, item := range byKey {
		result = append(result, item)
	}
	slices.SortFunc(result, func(a, b *BranchActivity) int {
		return cmp.Or(cmp.Compare(a.Date, b.Date), cmp.Compare(a.Project, b.Project), cmp.Compare(a.Branch, b.Branch))
	})
	return result
}
