package services

import (
	"testing"
	"time"

	"github.com/muety/wakapi/models"
	"github.com/stretchr/testify/assert"
)

func TestAggregateBranchActivity_SumsPerProjectDayAndBranch(t *testing.T) {
	tz, _ := time.LoadLocation("Europe/Zagreb")
	at := func(day, hour, minute int) models.CustomTime {
		return models.CustomTime(time.Date(2026, 10, day, hour, minute, 0, 0, tz))
	}
	durs := models.Durations{
		{Project: "app", Branch: "master", Time: at(7, 9, 0), Duration: 30 * time.Minute},
		{Project: "app", Branch: "feature/x", Time: at(7, 9, 30), Duration: 45 * time.Minute},
		{Project: "app", Branch: "master", Time: at(7, 11, 0), Duration: 15 * time.Minute},
		{Project: "other", Branch: "", Time: at(7, 12, 0), Duration: 10 * time.Minute},
	}
	from := time.Date(2026, 10, 7, 0, 0, 0, 0, tz)
	to := time.Date(2026, 10, 7, 23, 59, 59, 0, tz)

	res := AggregateBranchActivity(durs, from, to, tz)

	assert.Len(t, res, 3)
	assert.Equal(t, "app", res[0].Project)
	assert.Equal(t, "feature/x", res[0].Branch)
	assert.Equal(t, float64(45*60), res[0].Seconds)
	assert.Equal(t, "master", res[1].Branch)
	assert.Equal(t, float64(45*60), res[1].Seconds)
	assert.Equal(t, at(7, 9, 0).T(), res[1].FirstAt)
	assert.Equal(t, at(7, 11, 15).T(), res[1].LastAt)
	assert.Equal(t, "other", res[2].Project)
	assert.Equal(t, "", res[2].Branch)
	assert.Equal(t, "2026-10-07", res[2].Date)
}

func TestAggregateBranchActivity_SplitsAtLocalMidnightAndClipsRange(t *testing.T) {
	tz, _ := time.LoadLocation("Europe/Zagreb")
	durs := models.Durations{
		// 23:30 on the 7th to 00:45 on the 8th, local time
		{Project: "app", Branch: "feature/x", Time: models.CustomTime(time.Date(2026, 10, 7, 23, 30, 0, 0, tz)), Duration: 75 * time.Minute},
		// starts before the range
		{Project: "app", Branch: "master", Time: models.CustomTime(time.Date(2026, 10, 6, 23, 50, 0, 0, tz)), Duration: 20 * time.Minute},
	}
	from := time.Date(2026, 10, 7, 0, 0, 0, 0, tz)
	to := time.Date(2026, 10, 8, 23, 59, 59, 0, tz)

	res := AggregateBranchActivity(durs, from, to, tz)

	assert.Len(t, res, 3)
	assert.Equal(t, "2026-10-07", res[0].Date)
	assert.Equal(t, "feature/x", res[0].Branch)
	assert.Equal(t, float64(30*60), res[0].Seconds)
	assert.Equal(t, "2026-10-07", res[1].Date)
	assert.Equal(t, "master", res[1].Branch)
	assert.Equal(t, float64(10*60), res[1].Seconds)
	assert.Equal(t, "2026-10-08", res[2].Date)
	assert.Equal(t, float64(45*60), res[2].Seconds)
}
