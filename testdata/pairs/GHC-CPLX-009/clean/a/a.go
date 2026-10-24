package a

import "fmt"

// Summary describes the values.
func Summary(values []int, weight, limit int) string {
	total, count := 0, 0
	for _, v := range values {
		if v < 0 {
			continue
		}
		total += v * weight
		count++
	}
	if count == 0 {
		return "none"
	}
	mean := total / count
	label := fmt.Sprintf("%d items, mean %d", count, mean)
	if mean > limit {
		label += " (over the limit)"
	}
	spread := 0
	for _, v := range values {
		if d := v*weight - mean; d > spread {
			spread = d
		} else if -d > spread {
			spread = -d
		}
	}
	label += fmt.Sprintf(", spread %d", spread)
	return label
}

// Report describes the values, with a title.
func Report(values []int, weight, limit int, title string) string {
	return title + ": " + Summary(values, weight, limit)
}
