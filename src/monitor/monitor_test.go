package monitor

import (
	"testing"
	"time"

	"github.com/sleroq/reactor/src/db"
)

func TestCategoryOf(t *testing.T) {
	tests := []struct {
		name string
		msg  db.Message
		want messageCategory
	}{
		{name: "text", msg: db.Message{}, want: textCategory},
		{name: "photo", msg: db.Message{WithPhoto: true}, want: photoCategory},
		{name: "forwarded text", msg: db.Message{FwdFromUser: 1}, want: forwardCategory},
		{name: "forwarded photo", msg: db.Message{FwdFromChannel: 1, WithPhoto: true}, want: forwardCategory},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := categoryOf(test.msg); got != test.want {
				t.Fatalf("categoryOf() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestPercentileThreshold(t *testing.T) {
	tests := []struct {
		name         string
		ratings      []int
		totalSamples int
		targetCount  int
		minimum      int
		maximum      int
		want         int
	}{
		{name: "no samples", minimum: 23, maximum: 46, want: 23},
		{name: "not enough history", ratings: []int{30}, totalSamples: 21, targetCount: 21, minimum: 23, maximum: 46, want: 23},
		{name: "shared percentile", ratings: []int{10, 20, 30, 40}, totalSamples: 8, targetCount: 2, minimum: 1, maximum: 100, want: 30},
		{name: "minimum clamp", ratings: []int{1, 2, 3}, totalSamples: 10, targetCount: 1, minimum: 23, maximum: 46, want: 23},
		{name: "maximum clamp", ratings: []int{30, 50, 70}, totalSamples: 10, targetCount: 1, minimum: 23, maximum: 46, want: 46},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := percentileThreshold(test.ratings, test.totalSamples, test.targetCount, test.minimum, test.maximum)
			if got != test.want {
				t.Fatalf("percentileThreshold() = %d, want %d", got, test.want)
			}
		})
	}
}

func TestTextThresholdMinimum(t *testing.T) {
	tests := map[string]struct {
		threshold       int
		increasePercent int
		want            int
	}{
		"zero":                   {threshold: 0, increasePercent: 15, want: 0},
		"rounds up":              {threshold: 23, increasePercent: 15, want: 27},
		"exact default increase": {threshold: 40, increasePercent: 15, want: 46},
		"configured increase":    {threshold: 40, increasePercent: 25, want: 50},
	}

	for name, test := range tests {
		t.Run(name, func(t *testing.T) {
			if got := textThresholdMinimum(test.threshold, test.increasePercent); got != test.want {
				t.Fatalf("textThresholdMinimum(%d, %d) = %d, want %d", test.threshold, test.increasePercent, got, test.want)
			}
		})
	}
}

func TestRateMessageWithReplies_InitialSenderCanOptOut(t *testing.T) {
	message := db.Message{UserID: 1, Body: "мяу"}
	replies := []db.Message{
		{UserID: 1, Body: "мяу"},
		{UserID: 1, Body: "мяу мяу"},
		{UserID: 2, Body: "мяу"},
	}

	rating, err := rateMessageWithReplies(nil, replies, message, time.Time{})
	if err != nil {
		t.Fatalf("rateMessageWithReplies() error = %v", err)
	}
	if rating != -40 {
		t.Fatalf("rateMessageWithReplies() = %d, want -40", rating)
	}
}
