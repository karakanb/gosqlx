package ast

import (
	"sort"
	"strings"
)

func (p *PivotClause) SQL() string {
	values := strings.Join(p.InValues, ", ")
	if p.Dynamic {
		values = "ANY"
		if len(p.OrderBy) > 0 {
			values += " ORDER BY " + orderBySQL(p.OrderBy)
		}
	} else if p.ValueQuery != nil {
		values = stmtSQL(p.ValueQuery)
	}
	defaultValue := ""
	if p.DefaultOnNull != nil {
		defaultValue = " DEFAULT ON NULL (" + exprSQL(p.DefaultOnNull) + ")"
	}
	return "PIVOT (" + exprSQL(p.AggregateFunction) + " FOR " + p.PivotColumn + " IN (" + values + ")" + defaultValue + ")"
}

func (p *UnpivotClause) SQL() string {
	nulls := ""
	if p.IncludeNulls {
		nulls = " INCLUDE NULLS"
	}
	return "UNPIVOT" + nulls + " (" + p.ValueColumn + " FOR " + p.NameColumn + " IN (" + strings.Join(p.InColumns, ", ") + "))"
}

func (t *TimeTravelClause) SQL() string {
	keys := make([]string, 0, len(t.Named))
	for key := range t.Named {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	args := make([]string, 0, len(keys))
	for _, key := range keys {
		args = append(args, key+" => "+exprSQL(t.Named[key]))
	}
	text := t.Kind + " (" + strings.Join(args, ", ") + ")"
	for _, next := range t.Chained {
		text += " " + next.SQL()
	}
	return text
}

func (m *MatchRecognizeClause) SQL() string {
	parts := []string{}
	if len(m.PartitionBy) > 0 {
		parts = append(parts, "PARTITION BY "+exprListSQL(m.PartitionBy))
	}
	if len(m.OrderBy) > 0 {
		parts = append(parts, "ORDER BY "+orderBySQL(m.OrderBy))
	}
	if len(m.Measures) > 0 {
		measures := []string{}
		for _, measure := range m.Measures {
			measures = append(measures, exprSQL(measure.Expr)+" AS "+measure.Alias)
		}
		parts = append(parts, "MEASURES "+strings.Join(measures, ", "))
	}
	if m.RowsPerMatch != "" {
		parts = append(parts, m.RowsPerMatch)
	}
	if m.AfterMatch != "" {
		after := m.AfterMatch
		if !strings.HasPrefix(strings.ToUpper(after), "AFTER MATCH ") {
			after = "AFTER MATCH " + after
		}
		parts = append(parts, after)
	}
	parts = append(parts, "PATTERN ("+m.Pattern+")")
	if len(m.Definitions) > 0 {
		defs := []string{}
		for _, def := range m.Definitions {
			defs = append(defs, def.Name+" AS "+exprSQL(def.Condition))
		}
		parts = append(parts, "DEFINE "+strings.Join(defs, ", "))
	}
	return "MATCH_RECOGNIZE (" + strings.Join(parts, " ") + ")"
}
