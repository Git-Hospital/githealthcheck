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
func Report(xs []int, scale, max int, title string) string {
	sum, n := 0, 0
	for _, x := range xs {
		if x < 0 {
			continue
		}
		sum += x * scale
		n++
	}
	if n == 0 {
		return "none"
	}
	avg := sum / n
	text := fmt.Sprintf("%d items, mean %d", n, avg)
	if avg > max {
		text += " (over the limit)"
	}
	width := 0
	for _, x := range xs {
		if diff := x*scale - avg; diff > width {
			width = diff
		} else if -diff > width {
			width = -diff
		}
	}
	text += fmt.Sprintf(", spread %d", width)
	return title + ": " + text
}
