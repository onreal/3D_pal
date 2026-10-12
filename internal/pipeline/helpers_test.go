package pipeline_test

import "fmt"

func fmtSscan(s string, v *float64) (int, error) { return fmt.Sscan(s, v) }
