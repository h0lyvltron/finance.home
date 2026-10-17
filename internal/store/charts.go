package store

import (
	"encoding/json"
	"time"

	"finance.home/internal/models"
)

const (
	colorIncome     = "var:income"
	colorIncomeDim  = "var:income-dim"
	colorExpense    = "var:expense"
	colorExpenseDim = "var:expense-dim"
	colorNet        = "var:net"
	colorNetFill    = "var:net-fill"
	colorCompare    = "var:compare"
	colorBudget     = "var:budget"
	colorDanger     = "var:danger"
)

func dollars(cents int64) float64 {
	return float64(cents) / 100
}

func marshalChart(cfg map[string]any) string {
	b, err := json.Marshal(cfg)
	if err != nil {
		return "null"
	}
	return string(b)
}

func financeMeta(format string, share bool) map[string]any {
	m := map[string]any{"format": format}
	if share {
		m["share"] = true
	}
	return m
}

func pointRadius(n int) int {
	if n > 18 {
		return 0
	}
	return 3
}

func chartBase() map[string]any {
	return map[string]any{
		"responsive":          true,
		"maintainAspectRatio": false,
		"plugins": map[string]any{
			"legend": map[string]any{"display": true},
		},
	}
}

// CashFlowChart draws income upward, expense downward, and net as a line.
// Last-year compare, when present, is a dashed net line.
func CashFlowChart(points []CashFlowPoint, withCompare bool) string {
	labels := make([]string, len(points))
	income := make([]float64, len(points))
	expense := make([]float64, len(points))
	net := make([]float64, len(points))
	incomeColor := make([]string, len(points))
	expenseColor := make([]string, len(points))
	var compare []any
	if withCompare {
		compare = make([]any, len(points))
	}
	for i, p := range points {
		labels[i] = p.Label
		if p.InProgress {
			labels[i] += " *"
			incomeColor[i] = colorIncomeDim
			expenseColor[i] = colorExpenseDim
		} else {
			incomeColor[i] = colorIncome
			expenseColor[i] = colorExpense
		}
		income[i] = dollars(p.Income)
		expense[i] = -dollars(p.Expense)
		net[i] = dollars(p.Net)
		if withCompare {
			if p.HasCompare {
				compare[i] = dollars(p.CompareNet)
			}
		}
	}
	radius := pointRadius(len(points))
	datasets := []map[string]any{
		{
			"type":            "bar",
			"label":           "Income",
			"data":            income,
			"backgroundColor": incomeColor,
			"borderRadius":    4,
			"order":           2,
		},
		{
			"type":            "bar",
			"label":           "Expense",
			"data":            expense,
			"backgroundColor": expenseColor,
			"borderRadius":    4,
			"order":           2,
		},
		{
			"type":             "line",
			"label":            "Net",
			"data":             net,
			"borderColor":      colorNet,
			"backgroundColor":  "transparent",
			"borderWidth":      2,
			"tension":          0,
			"pointRadius":      radius,
			"pointHoverRadius": 4,
			"fill":             false,
			"order":            1,
		},
	}
	if withCompare {
		datasets = append(datasets, map[string]any{
			"type":            "line",
			"label":           "Net last year",
			"data":            compare,
			"borderColor":     colorCompare,
			"backgroundColor": "transparent",
			"borderWidth":     2,
			"borderDash":      []int{5, 4},
			"tension":         0,
			"pointRadius":     0,
			"fill":            false,
			"spanGaps":        false,
			"order":           0,
		})
	}
	opts := chartBase()
	opts["scales"] = map[string]any{
		"y": map[string]any{"beginAtZero": true},
	}
	return marshalChart(map[string]any{
		"type": "bar",
		"data": map[string]any{
			"labels":   labels,
			"datasets": datasets,
		},
		"options": opts,
		"finance": financeMeta("currency", false),
	})
}

// CategoryChart is a horizontal ranking of spend. Amounts are positive magnitudes.
func CategoryChart(segs []SegmentTotal, withCompare bool) string {
	labels := make([]string, len(segs))
	data := make([]float64, len(segs))
	compare := make([]float64, len(segs))
	for i, s := range segs {
		labels[i] = s.Label
		data[i] = dollars(s.Amount)
		if s.HasCompare {
			compare[i] = dollars(s.CompareAmount)
		}
	}
	datasets := []map[string]any{{
		"label":           "Spend",
		"data":            data,
		"backgroundColor": colorExpense,
		"borderRadius":    6,
	}}
	if withCompare {
		datasets = append(datasets, map[string]any{
			"label":           "Last year",
			"data":            compare,
			"backgroundColor": colorCompare,
			"borderRadius":    6,
		})
	}
	opts := chartBase()
	opts["indexAxis"] = "y"
	opts["plugins"] = map[string]any{
		"legend": map[string]any{"display": withCompare},
	}
	opts["scales"] = map[string]any{
		"x": map[string]any{"beginAtZero": true},
	}
	return marshalChart(map[string]any{
		"type": "bar",
		"data": map[string]any{
			"labels":   labels,
			"datasets": datasets,
		},
		"options": opts,
		"finance": financeMeta("currency", true),
	})
}

// PivotChart stacks the top categories and overlays total spend.
func PivotChart(pt *PivotTable) string {
	if pt == nil {
		return "null"
	}
	rows := pt.RowKeys
	labels := pt.RowLabels
	if len(rows) > 8 {
		rows = rows[:8]
		labels = labels[:8]
	}
	palette := []string{
		colorExpense,
		colorIncome,
		"var:budget",
		colorCompare,
		colorDanger,
		colorIncomeDim,
		colorExpenseDim,
		colorNet,
	}
	datasets := make([]map[string]any, 0, len(rows)+1)
	for i, rk := range rows {
		data := make([]float64, len(pt.ColKeys))
		for j, ck := range pt.ColKeys {
			if cell, ok := pt.Cells[rk][ck]; ok {
				data[j] = dollars(cell.Amount)
			}
		}
		name := rk
		if i < len(labels) {
			name = labels[i]
		}
		datasets = append(datasets, map[string]any{
			"type":            "bar",
			"label":           name,
			"data":            data,
			"backgroundColor": palette[i%len(palette)],
			"stack":           "spend",
			"order":           2,
		})
	}
	totals := make([]float64, len(pt.ColKeys))
	colLabels := make([]string, len(pt.ColKeys))
	for i, ck := range pt.ColKeys {
		totals[i] = dollars(pt.ColTotals[ck])
		colLabels[i] = ""
		if i < len(pt.ColLabels) {
			colLabels[i] = pt.ColLabels[i]
		}
		if ck == pt.InProgCol {
			colLabels[i] += " *"
		}
	}
	datasets = append(datasets, map[string]any{
		"type":            "line",
		"label":           "Total",
		"data":            totals,
		"borderColor":     colorNet,
		"backgroundColor": "transparent",
		"borderWidth":     2,
		"tension":         0,
		"pointRadius":     pointRadius(len(pt.ColKeys)),
		"fill":            false,
		"order":           1,
		"yAxisID":         "y1",
	})
	opts := chartBase()
	opts["scales"] = map[string]any{
		"x":  map[string]any{"stacked": true},
		"y":  map[string]any{"stacked": true, "beginAtZero": true},
		"y1": map[string]any{"display": false, "beginAtZero": true, "stacked": false},
	}
	return marshalChart(map[string]any{
		"type": "bar",
		"data": map[string]any{
			"labels":   colLabels,
			"datasets": datasets,
		},
		"options": opts,
		"finance": financeMeta("currency", false),
	})
}

// NetWorthChart is the household total only. Account lines stay off this axis.
func NetWorthChart(series models.NetWorthSeries) string {
	labels := netWorthLabels(series.Points)
	data := make([]float64, len(series.Points))
	for i, p := range series.Points {
		data[i] = dollars(p.Amount)
	}
	opts := chartBase()
	opts["plugins"] = map[string]any{"legend": map[string]any{"display": false}}
	opts["scales"] = map[string]any{
		"y": map[string]any{"beginAtZero": false},
	}
	return marshalChart(map[string]any{
		"type": "line",
		"data": map[string]any{
			"labels": labels,
			"datasets": []map[string]any{{
				"label":            "Net worth",
				"data":             data,
				"borderColor":      colorNet,
				"backgroundColor":  colorNetFill,
				"fill":             true,
				"tension":          0.25,
				"pointRadius":      pointRadius(len(series.Points)),
				"pointHoverRadius": 4,
				"borderWidth":      2,
			}},
		},
		"options": opts,
		"finance": financeMeta("currency", false),
	})
}

func netWorthLabels(points []models.BalancePoint) []string {
	multiYear := false
	if len(points) > 1 {
		a, e1 := time.Parse("2006-01-02", points[0].Date)
		b, e2 := time.Parse("2006-01-02", points[len(points)-1].Date)
		if e1 == nil && e2 == nil && a.Year() != b.Year() {
			multiYear = true
		}
	}
	labels := make([]string, len(points))
	for i, p := range points {
		t, err := time.Parse("2006-01-02", p.Date)
		if err != nil {
			if p.Label != "" {
				labels[i] = p.Label
			} else {
				labels[i] = p.Date
			}
			continue
		}
		if multiYear {
			labels[i] = t.Format("Jan 2 2006")
		} else {
			labels[i] = t.Format("Jan 2")
		}
	}
	return labels
}

// BudgetChart ranks categories by how far actual spend is over the target.
func BudgetChart(lines []models.BudgetLine) string {
	picked := make([]models.BudgetLine, 0, len(lines))
	for _, ln := range lines {
		if ln.HasChildren || (ln.AmountCents <= 0 && ln.SpentCents <= 0) {
			continue
		}
		picked = append(picked, ln)
	}
	// Most over budget first. The category axis is reversed so that row is at the top.
	for i := 1; i < len(picked); i++ {
		j := i
		for j > 0 {
			overJ := picked[j].SpentCents - picked[j].AmountCents
			overPrev := picked[j-1].SpentCents - picked[j-1].AmountCents
			if overJ <= overPrev {
				break
			}
			picked[j], picked[j-1] = picked[j-1], picked[j]
			j--
		}
	}
	if len(picked) > 12 {
		picked = picked[:12]
	}
	labels := make([]string, len(picked))
	budgeted := make([]float64, len(picked))
	spent := make([]float64, len(picked))
	spentColor := make([]string, len(picked))
	for i, ln := range picked {
		labels[i] = ln.CategoryName
		budgeted[i] = dollars(ln.AmountCents)
		spent[i] = dollars(ln.SpentCents)
		if ln.SpentCents > ln.AmountCents {
			spentColor[i] = colorDanger
		} else {
			spentColor[i] = colorExpense
		}
	}
	opts := chartBase()
	opts["indexAxis"] = "y"
	opts["scales"] = map[string]any{
		"x": map[string]any{"beginAtZero": true},
		"y": map[string]any{"reverse": true},
	}
	return marshalChart(map[string]any{
		"type": "bar",
		"data": map[string]any{
			"labels": labels,
			"datasets": []map[string]any{
				{"label": "Budget", "data": budgeted, "backgroundColor": colorBudget, "borderRadius": 4},
				{"label": "Actual", "data": spent, "backgroundColor": spentColor, "borderRadius": 4},
			},
		},
		"options": opts,
		"finance": financeMeta("currency", false),
	})
}

// SavingsRateChart is the monthly savings rate, with the household target rate when one is set.
func SavingsRateChart(sr models.SavingsRateReport) string {
	labels := make([]string, len(sr.Points))
	rates := make([]any, len(sr.Points))
	target := make([]float64, len(sr.Points))
	showTarget := sr.HasTarget && sr.TargetRatePct > 0 && len(sr.Points) > 0
	for i, p := range sr.Points {
		labels[i] = p.Label
		if p.HasIncome {
			rates[i] = p.RatePct
		}
		if showTarget {
			target[i] = sr.TargetRatePct
		}
	}
	datasets := []map[string]any{{
		"label":            "Savings rate",
		"data":             rates,
		"borderColor":      colorNet,
		"backgroundColor":  colorNetFill,
		"fill":             true,
		"tension":          0,
		"pointRadius":      pointRadius(len(sr.Points)),
		"pointHoverRadius": 4,
		"borderWidth":      2,
		"spanGaps":         false,
	}}
	if showTarget {
		datasets = append(datasets, map[string]any{
			"label":           "Target",
			"data":            target,
			"borderColor":     colorCompare,
			"backgroundColor": "transparent",
			"borderDash":      []int{5, 4},
			"borderWidth":     2,
			"pointRadius":     0,
			"fill":            false,
		})
	}
	opts := chartBase()
	opts["plugins"] = map[string]any{
		"legend": map[string]any{"display": showTarget},
	}
	opts["scales"] = map[string]any{
		"y": map[string]any{"beginAtZero": true},
	}
	return marshalChart(map[string]any{
		"type": "line",
		"data": map[string]any{
			"labels":   labels,
			"datasets": datasets,
		},
		"options": opts,
		"finance": financeMeta("percent", false),
	})
}
