package httpx

const (
	hnTreeIndentFullPx    = 40
	hnTreeIndentTightPx   = 14
	hnTreeIndentFullSteps = 5 // first five indent steps at full width; deeper uses tight (HN-style)
)

func hnTreeIndentPx(depth int) int {
	if depth <= 1 {
		return 0
	}
	d := depth - 1
	if d <= hnTreeIndentFullSteps {
		return d * hnTreeIndentFullPx
	}
	return hnTreeIndentFullSteps*hnTreeIndentFullPx + (d-hnTreeIndentFullSteps)*hnTreeIndentTightPx
}

func hnPathIndentPx(index int) int {
	if index <= 0 {
		return 0
	}
	return index * hnTreeIndentFullPx
}
