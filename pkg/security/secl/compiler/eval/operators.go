// Unless explicitly stated otherwise all files in this repository are licensed
// under the Apache License Version 2.0.
// This product includes software developed at Datadog (https://www.datadoghq.com/).
// Copyright 2016-present Datadog, Inc.

// Package eval holds eval related files
package eval

import (
	"net"
	"strings"
)

// OpOverrides defines operator override functions
type OpOverrides struct {
	StringEquals         func(a *StringEvaluator, b *StringEvaluator, state *State) (*BoolEvaluator, error)
	StringValuesContains func(a *StringEvaluator, b *StringValuesEvaluator, state *State) (*BoolEvaluator, error)
	StringArrayContains  func(a *StringEvaluator, b *StringArrayEvaluator, state *State) (*BoolEvaluator, error)
	StringArrayMatches   func(a *StringArrayEvaluator, b *StringValuesEvaluator, state *State) (*BoolEvaluator, error)
}

// return whether a arithmetic operation is deterministic
func isArithmDeterministic(a Evaluator, b Evaluator, state *State) bool {
	isDc := a.IsDeterministicFor(state.field) || b.IsDeterministicFor(state.field)

	if aField := a.GetField(); aField != "" && state.field != "" && aField != state.field {
		isDc = false
	}
	if bField := b.GetField(); bField != "" && state.field != "" && bField != state.field {
		isDc = false
	}

	return isDc
}

// Or operator
func Or(a *BoolEvaluator, b *BoolEvaluator, state *State) (*BoolEvaluator, error) {
	isDc := a.IsDeterministicFor(state.field) || b.IsDeterministicFor(state.field)

	if a.EvalFnc != nil && b.EvalFnc != nil {
		ea, eb := a.EvalFnc, b.EvalFnc

		if state.field != "" {
			if !a.IsDeterministicFor(state.field) && !a.IsStatic() {
				ea = func(*Context) bool {
					return true
				}
			}
			if !b.IsDeterministicFor(state.field) && !b.IsStatic() {
				eb = func(*Context) bool {
					return true
				}
			}
		}

		if a.Weight > b.Weight {
			tmp := ea
			ea = eb
			eb = tmp
		}

		evalFnc := func(ctx *Context) bool {
			if ea(ctx) {
				if a.Field != "" {
					ctx.AddMatchingSubExpr(MatchingValue{Field: a.Field, Value: true, Offset: a.Offset}, MatchingValue{})
				}
				return true
			}
			if eb(ctx) {
				if b.Field != "" {
					ctx.AddMatchingSubExpr(MatchingValue{}, MatchingValue{Field: b.Field, Value: true, Offset: b.Offset})
				}
				return true
			}
			return false
		}

		return &BoolEvaluator{
			EvalFnc:         evalFnc,
			Weight:          a.Weight + b.Weight,
			isDeterministic: isDc,
		}, nil
	}

	if a.EvalFnc == nil && b.EvalFnc == nil {
		ea, eb := a.Value, b.Value

		return &BoolEvaluator{
			Value:           ea || eb,
			isDeterministic: isDc,
		}, nil
	}

	if a.EvalFnc != nil {
		ea, eb := a.EvalFnc, b.Value

		if a.Field != "" {
			if err := state.UpdateFieldValues(a.Field, FieldValue{Value: eb, Type: ScalarValueType}); err != nil {
				return nil, err
			}
		}

		if state.field != "" {
			if !a.IsDeterministicFor(state.field) && !a.IsStatic() {
				ea = func(*Context) bool {
					return true
				}
			}
			if !b.IsDeterministicFor(state.field) && !b.IsStatic() {
				eb = true
			}
		}

		evalFnc := func(ctx *Context) bool {
			va, vb := ea(ctx), eb
			res := va || vb
			if res && a.Field != "" {
				ctx.AddMatchingSubExpr(MatchingValue{Field: a.Field, Value: va, Offset: a.Offset}, MatchingValue{})
			}
			return res
		}

		return &BoolEvaluator{
			EvalFnc:         evalFnc,
			Weight:          a.Weight,
			isDeterministic: isDc,
		}, nil
	}

	ea, eb := a.Value, b.EvalFnc

	if b.Field != "" {
		if err := state.UpdateFieldValues(b.Field, FieldValue{Value: ea, Type: ScalarValueType}); err != nil {
			return nil, err
		}
	}

	if state.field != "" {
		if !a.IsDeterministicFor(state.field) && !a.IsStatic() {
			ea = true
		}
		if !b.IsDeterministicFor(state.field) && !b.IsStatic() {
			eb = func(*Context) bool {
				return true
			}
		}
	}

	evalFnc := func(ctx *Context) bool {
		va, vb := ea, eb(ctx)
		res := va || vb
		if res && b.Field != "" {
			ctx.AddMatchingSubExpr(MatchingValue{}, MatchingValue{Field: b.Field, Value: eb, Offset: b.Offset})
		}
		return res
	}

	return &BoolEvaluator{
		EvalFnc:         evalFnc,
		Weight:          b.Weight,
		isDeterministic: isDc,
	}, nil
}

// And operator
func And(a *BoolEvaluator, b *BoolEvaluator, state *State) (*BoolEvaluator, error) {
	isDc := a.IsDeterministicFor(state.field) || b.IsDeterministicFor(state.field)

	if a.EvalFnc != nil && b.EvalFnc != nil {
		ea, eb := a.EvalFnc, b.EvalFnc

		if state.field != "" {
			if !a.IsDeterministicFor(state.field) && !a.IsStatic() {
				ea = func(*Context) bool {
					return true
				}
			}
			if !b.IsDeterministicFor(state.field) && !b.IsStatic() {
				eb = func(*Context) bool {
					return true
				}
			}
		}

		if a.Weight > b.Weight {
			tmp := ea
			ea = eb
			eb = tmp
		}

		evalFnc := func(ctx *Context) bool {
			res := ea(ctx) && eb(ctx)
			if res {
				if a.Field != "" {
					ctx.AddMatchingSubExpr(MatchingValue{Field: a.Field, Value: ea(ctx), Offset: a.Offset}, MatchingValue{})
				}
				if b.Field != "" {
					ctx.AddMatchingSubExpr(MatchingValue{}, MatchingValue{Field: b.Field, Value: eb(ctx), Offset: b.Offset})

				}
			}
			return res
		}

		return &BoolEvaluator{
			EvalFnc:         evalFnc,
			Weight:          a.Weight + b.Weight,
			isDeterministic: isDc,
		}, nil
	}

	if a.EvalFnc == nil && b.EvalFnc == nil {
		ea, eb := a.Value, b.Value

		return &BoolEvaluator{
			Value:           ea && eb,
			isDeterministic: isDc,
		}, nil
	}

	if a.EvalFnc != nil {
		ea, eb := a.EvalFnc, b.Value

		if a.Field != "" {
			if err := state.UpdateFieldValues(a.Field, FieldValue{Value: eb, Type: ScalarValueType}); err != nil {
				return nil, err
			}
		}

		if state.field != "" {
			if !a.IsDeterministicFor(state.field) && !a.IsStatic() {
				ea = func(*Context) bool {
					return true
				}
			}
			if !b.IsDeterministicFor(state.field) && !b.IsStatic() {
				eb = true
			}
		}

		evalFnc := func(ctx *Context) bool {
			res := ea(ctx) && eb
			if res && a.Field != "" {
				ctx.AddMatchingSubExpr(MatchingValue{Field: a.Field, Value: ea(ctx), Offset: a.Offset}, MatchingValue{})
			}
			return res
		}

		return &BoolEvaluator{
			EvalFnc:         evalFnc,
			Weight:          a.Weight,
			isDeterministic: isDc,
		}, nil
	}

	ea, eb := a.Value, b.EvalFnc

	if b.Field != "" {
		if err := state.UpdateFieldValues(b.Field, FieldValue{Value: ea, Type: ScalarValueType}); err != nil {
			return nil, err
		}
	}

	if state.field != "" {
		if !a.IsDeterministicFor(state.field) && !a.IsStatic() {
			ea = true
		}
		if !b.IsDeterministicFor(state.field) && !b.IsStatic() {
			eb = func(_ *Context) bool {
				return true
			}
		}
	}

	evalFnc := func(ctx *Context) bool {
		res := ea && eb(ctx)
		if res && b.Field != "" {
			ctx.AddMatchingSubExpr(MatchingValue{}, MatchingValue{Field: b.Field, Value: eb(ctx), Offset: b.Offset})
		}
		return res
	}

	return &BoolEvaluator{
		EvalFnc:         evalFnc,
		Weight:          b.Weight,
		isDeterministic: isDc,
	}, nil
}

// IntNot ^int operator
func IntNot(a *IntEvaluator, state *State) *IntEvaluator {
	isDc := a.IsDeterministicFor(state.field)

	if a.EvalFnc != nil {
		ea := a.EvalFnc

		evalFnc := func(ctx *Context) int {
			return ^ea(ctx)
		}

		return &IntEvaluator{
			EvalFnc:         evalFnc,
			Weight:          a.Weight,
			isDeterministic: isDc,
			originField:     a.OriginField(),
		}
	}

	return &IntEvaluator{
		Value:           ^a.Value,
		Weight:          a.Weight,
		isDeterministic: isDc,
	}
}

// StringEquals evaluates string
func StringEquals(a *StringEvaluator, b *StringEvaluator, state *State) (*BoolEvaluator, error) {
	isDc := isArithmDeterministic(a, b, state)

	if a.Field != "" {
		if err := state.UpdateFieldValues(a.Field, FieldValue{Value: b.Value, Type: b.ValueType}); err != nil {
			return nil, err
		}
	}

	if b.Field != "" {
		if err := state.UpdateFieldValues(b.Field, FieldValue{Value: a.Value, Type: a.ValueType}); err != nil {
			return nil, err
		}
	}

	// default comparison
	op := func(as string, bs string) bool {
		return as == bs
	}

	if a.Field != "" && b.Field != "" {
		if a.StringCmpOpts.CaseInsensitive || b.StringCmpOpts.CaseInsensitive {
			op = strings.EqualFold
		}
	} else if a.Field != "" {
		matcher, err := b.ToStringMatcher(a.StringCmpOpts)
		if err != nil {
			return nil, err
		}

		if matcher != nil {
			op = func(as string, _ string) bool {
				return matcher.Matches(as)
			}
		}
	} else if b.Field != "" {
		matcher, err := a.ToStringMatcher(b.StringCmpOpts)
		if err != nil {
			return nil, err
		}

		if matcher != nil {
			op = func(_ string, bs string) bool {
				return matcher.Matches(bs)
			}
		}
	}

	if a.EvalFnc != nil && b.EvalFnc != nil {
		ea, eb := a.EvalFnc, b.EvalFnc

		evalFnc := func(ctx *Context) bool {
			va, vb := ea(ctx), eb(ctx)
			res := op(va, vb)
			if res {
				ctx.AddMatchingSubExpr(MatchingValue{Field: a.Field, Value: va, Offset: a.Offset}, MatchingValue{Field: b.Field, Value: eb, Offset: b.Offset})
			}
			return res
		}

		return &BoolEvaluator{
			EvalFnc:         evalFnc,
			Weight:          a.Weight + b.Weight,
			isDeterministic: isDc,
		}, nil
	}

	if a.EvalFnc == nil && b.EvalFnc == nil {
		ea, eb := a.Value, b.Value

		return &BoolEvaluator{
			Value:           op(ea, eb),
			isDeterministic: isDc,
		}, nil
	}

	if a.EvalFnc != nil {
		ea, eb := a.EvalFnc, b.Value

		evalFnc := func(ctx *Context) bool {
			va, vb := ea(ctx), eb
			res := op(va, vb)
			if res {
				ctx.AddMatchingSubExpr(MatchingValue{Field: a.Field, Value: va, Offset: a.Offset}, MatchingValue{Value: vb, Offset: b.Offset})
			}
			return res
		}

		return &BoolEvaluator{
			EvalFnc:         evalFnc,
			Weight:          a.Weight,
			isDeterministic: isDc,
		}, nil
	}

	ea, eb := a.Value, b.EvalFnc

	evalFnc := func(ctx *Context) bool {
		va, vb := ea, eb(ctx)
		res := op(ea, vb)
		if res {
			ctx.AddMatchingSubExpr(MatchingValue{Value: va, Offset: a.Offset}, MatchingValue{Field: b.Field, Value: vb, Offset: b.Offset})
		}
		return res
	}

	return &BoolEvaluator{
		EvalFnc:         evalFnc,
		Weight:          b.Weight,
		isDeterministic: isDc,
	}, nil
}

// Not !true operator
func Not(a *BoolEvaluator, state *State) *BoolEvaluator {
	isDc := a.IsDeterministicFor(state.field)

	if a.EvalFnc != nil {
		ea := func(ctx *Context) bool {
			return !a.EvalFnc(ctx)
		}

		if state.field != "" && !a.IsDeterministicFor(state.field) {
			ea = func(_ *Context) bool {
				return true
			}
		}

		return &BoolEvaluator{
			EvalFnc:         ea,
			Weight:          a.Weight,
			isDeterministic: isDc,
			originField:     a.OriginField(),
		}
	}

	return &BoolEvaluator{
		Value:           !a.Value,
		Weight:          a.Weight,
		isDeterministic: isDc,
	}
}

// Minus -int operator
func Minus(a *IntEvaluator, state *State) *IntEvaluator {
	isDc := a.IsDeterministicFor(state.field)

	if a.EvalFnc != nil {
		ea := a.EvalFnc

		evalFnc := func(ctx *Context) int {
			return -ea(ctx)
		}

		return &IntEvaluator{
			EvalFnc:         evalFnc,
			Weight:          a.Weight,
			isDeterministic: isDc,
			originField:     a.OriginField(),
		}
	}

	return &IntEvaluator{
		Value:           -a.Value,
		Weight:          a.Weight,
		isDeterministic: isDc,
	}
}

// StringArrayContains evaluates array of strings against a value
func StringArrayContains(a *StringEvaluator, b *StringArrayEvaluator, state *State) (*BoolEvaluator, error) {
	isDc := isArithmDeterministic(a, b, state)

	if a.Field != "" {
		for _, value := range b.Values {
			if err := state.UpdateFieldValues(a.Field, FieldValue{Value: value, Type: ScalarValueType}); err != nil {
				return nil, err
			}
		}
	}

	if b.Field != "" {
		if err := state.UpdateFieldValues(b.Field, FieldValue{Value: a.Value, Type: a.ValueType}); err != nil {
			return nil, err
		}
	}

	op := func(a string, b []string, cmp func(a, b string) bool) bool {
		for _, bs := range b {
			if cmp(a, bs) {
				return true
			}
		}
		return false
	}

	cmp := func(a, b string) bool {
		return a == b
	}

	if a.Field != "" && b.Field != "" {
		if a.StringCmpOpts.CaseInsensitive || b.StringCmpOpts.CaseInsensitive {
			cmp = strings.EqualFold
		}
	} else if a.Field != "" && a.StringCmpOpts.CaseInsensitive {
		cmp = strings.EqualFold
	} else if b.Field != "" {
		matcher, err := a.ToStringMatcher(b.StringCmpOpts)
		if err != nil {
			return nil, err
		}

		if matcher != nil {
			cmp = func(_, b string) bool {
				return matcher.Matches(b)
			}
		}
	}

	if a.EvalFnc != nil && b.EvalFnc != nil {
		ea, eb := a.EvalFnc, b.EvalFnc

		evalFnc := func(ctx *Context) bool {
			va, vb := ea(ctx), eb(ctx)
			res := op(va, vb, cmp)
			if res {
				ctx.AddMatchingSubExpr(MatchingValue{Field: a.Field, Value: va, Offset: a.Offset}, MatchingValue{Field: b.Field, Value: vb, Offset: b.Offset})
			}
			return res
		}

		return &BoolEvaluator{
			EvalFnc:         evalFnc,
			Weight:          a.Weight + b.Weight,
			isDeterministic: isDc,
		}, nil
	}

	if a.EvalFnc == nil && b.EvalFnc == nil {
		ea, eb := a.Value, b.Values

		return &BoolEvaluator{
			Value:           op(ea, eb, cmp),
			Weight:          a.Weight + InArrayWeight*len(eb),
			isDeterministic: isDc,
		}, nil
	}

	if a.EvalFnc != nil {
		ea, eb := a.EvalFnc, b.Values

		evalFnc := func(ctx *Context) bool {
			va, vb := ea(ctx), eb
			res := op(va, vb, cmp)
			if res {
				ctx.AddMatchingSubExpr(MatchingValue{Field: a.Field, Value: va, Offset: a.Offset}, MatchingValue{Value: vb, Offset: b.Offset})
			}
			return res
		}

		return &BoolEvaluator{
			EvalFnc:         evalFnc,
			Weight:          a.Weight + InArrayWeight*len(eb),
			isDeterministic: isDc,
		}, nil
	}

	ea, eb := a.Value, b.EvalFnc

	evalFnc := func(ctx *Context) bool {
		va, vb := ea, eb(ctx)
		res := op(va, vb, cmp)
		if res {
			ctx.AddMatchingSubExpr(MatchingValue{Value: va, Offset: a.Offset}, MatchingValue{Field: b.Field, Value: vb, Offset: b.Offset})
		}
		return res
	}

	return &BoolEvaluator{
		EvalFnc:         evalFnc,
		Weight:          b.Weight,
		isDeterministic: isDc,
	}, nil
}

// StringValuesContains evaluates a string against values
func StringValuesContains(a *StringEvaluator, b *StringValuesEvaluator, state *State) (*BoolEvaluator, error) {
	isDc := isArithmDeterministic(a, b, state)

	if a.Field != "" {
		for _, value := range b.Values.fieldValues {
			if err := state.UpdateFieldValues(a.Field, value); err != nil {
				return nil, err
			}
		}
	}

	if err := b.Compile(a.StringCmpOpts); err != nil {
		return nil, err
	}

	if a.EvalFnc != nil && b.EvalFnc != nil {
		ea, eb := a.EvalFnc, b.EvalFnc

		evalFnc := func(ctx *Context) bool {
			va, vb := ea(ctx), eb(ctx)
			res, vm := vb.Matches(va)
			if res {
				ctx.AddMatchingSubExpr(MatchingValue{Field: a.Field, Value: va, Offset: a.Offset}, MatchingValue{Value: vm, Offset: b.Offset})
			}
			return res
		}

		return &BoolEvaluator{
			EvalFnc:         evalFnc,
			Weight:          a.Weight + b.Weight,
			isDeterministic: isDc,
		}, nil
	}

	if a.EvalFnc == nil && b.EvalFnc == nil {
		ea, eb := a.Value, b.Values
		res, _ := eb.Matches(ea)

		return &BoolEvaluator{
			Value:           res,
			Weight:          a.Weight + InArrayWeight*len(eb.fieldValues),
			isDeterministic: isDc,
		}, nil
	}

	if a.EvalFnc != nil {
		ea, eb := a.EvalFnc, b.Values

		evalFnc := func(ctx *Context) bool {
			va, vb := ea(ctx), eb
			res, vm := vb.Matches(va)
			if res {
				ctx.AddMatchingSubExpr(MatchingValue{Field: a.Field, Value: va, Offset: a.Offset}, MatchingValue{Value: vm, Offset: b.Offset})
			}
			return res
		}

		return &BoolEvaluator{
			EvalFnc:         evalFnc,
			Weight:          a.Weight + InArrayWeight*len(eb.fieldValues),
			isDeterministic: isDc,
		}, nil
	}

	ea, eb := a.Value, b.EvalFnc

	evalFnc := func(ctx *Context) bool {
		va, vb := ea, eb(ctx)
		res, _ := vb.Matches(va)
		return res
	}

	return &BoolEvaluator{
		EvalFnc:         evalFnc,
		Weight:          b.Weight,
		isDeterministic: isDc,
	}, nil
}

// StringArrayMatches weak comparison, a least one element of a should be in b. a can't contain regexp
func StringArrayMatches(a *StringArrayEvaluator, b *StringValuesEvaluator, state *State) (*BoolEvaluator, error) {
	isDc := isArithmDeterministic(a, b, state)

	if a.Field != "" {
		for _, value := range b.Values.fieldValues {
			if err := state.UpdateFieldValues(a.Field, value); err != nil {
				return nil, err
			}
		}
	}

	if err := b.Compile(a.StringCmpOpts); err != nil {
		return nil, err
	}

	arrayOp := func(a []string, b *StringValues) (bool, string) {
		for _, as := range a {
			if ok, vm := b.Matches(as); ok {
				return true, vm
			}
		}
		return false, ""
	}

	if a.EvalFnc != nil && b.EvalFnc != nil {
		ea, eb := a.EvalFnc, b.EvalFnc

		evalFnc := func(ctx *Context) bool {
			va, vb := ea(ctx), eb(ctx)
			res, vm := arrayOp(va, vb)
			if res {
				ctx.AddMatchingSubExpr(MatchingValue{Field: a.Field, Value: va, Offset: a.Offset}, MatchingValue{Value: vm, Offset: b.Offset})
			}
			return res
		}

		return &BoolEvaluator{
			EvalFnc:         evalFnc,
			Weight:          a.Weight + b.Weight,
			isDeterministic: isDc,
		}, nil
	}

	if a.EvalFnc == nil && b.EvalFnc == nil {
		ea, eb := a.Values, b.Values
		res, _ := arrayOp(ea, &eb)

		return &BoolEvaluator{
			Value:           res,
			Weight:          a.Weight + InArrayWeight*len(eb.fieldValues),
			isDeterministic: isDc,
		}, nil
	}

	if a.EvalFnc != nil {
		ea, eb := a.EvalFnc, b.Values

		evalFnc := func(ctx *Context) bool {
			va, vb := ea(ctx), &eb
			res, vm := arrayOp(va, vb)
			if res {
				ctx.AddMatchingSubExpr(MatchingValue{Field: a.Field, Value: va, Offset: a.Offset}, MatchingValue{Value: vm, Offset: b.Offset})
			}
			return res
		}

		return &BoolEvaluator{
			EvalFnc:         evalFnc,
			Weight:          a.Weight + InArrayWeight*len(eb.fieldValues),
			isDeterministic: isDc,
		}, nil
	}

	ea, eb := a.Values, b.EvalFnc

	evalFnc := func(ctx *Context) bool {
		res, _ := arrayOp(ea, eb(ctx))
		return res
	}

	return &BoolEvaluator{
		EvalFnc:         evalFnc,
		Weight:          b.Weight,
		isDeterministic: isDc,
	}, nil
}

// IntArrayMatches weak comparison, a least one element of a should be in b
func IntArrayMatches(a *IntArrayEvaluator, b *IntArrayEvaluator, state *State) (*BoolEvaluator, error) {
	isDc := isArithmDeterministic(a, b, state)

	if a.Field != "" {
		for _, value := range b.Values {
			if err := state.UpdateFieldValues(a.Field, FieldValue{Value: value}); err != nil {
				return nil, err
			}
		}
	}

	arrayOp := func(a []int, b []int) (bool, int) {
		for _, va := range a {
			for _, vb := range b {
				if va == vb {
					return true, vb
				}
			}
		}
		return false, 0
	}

	if a.EvalFnc != nil && b.EvalFnc != nil {
		ea, eb := a.EvalFnc, b.EvalFnc

		evalFnc := func(ctx *Context) bool {
			va, vb := ea(ctx), eb(ctx)
			res, vm := arrayOp(va, vb)
			if res {
				ctx.AddMatchingSubExpr(MatchingValue{Field: a.Field, Value: va, Offset: a.Offset}, MatchingValue{Value: vm, Offset: b.Offset})
			}
			return res
		}

		return &BoolEvaluator{
			EvalFnc:         evalFnc,
			Weight:          a.Weight + b.Weight,
			isDeterministic: isDc,
		}, nil
	}

	if a.EvalFnc == nil && b.EvalFnc == nil {
		ea, eb := a.Values, b.Values
		res, _ := arrayOp(ea, eb)

		return &BoolEvaluator{
			Value:           res,
			Weight:          a.Weight + InArrayWeight*len(eb),
			isDeterministic: isDc,
		}, nil
	}

	if a.EvalFnc != nil {
		ea, eb := a.EvalFnc, b.Values

		evalFnc := func(ctx *Context) bool {
			va, vb := ea(ctx), eb
			res, vm := arrayOp(va, vb)
			if res {
				ctx.AddMatchingSubExpr(MatchingValue{Field: a.Field, Value: va, Offset: a.Offset}, MatchingValue{Value: vm, Offset: b.Offset})
			}
			return res
		}

		return &BoolEvaluator{
			EvalFnc:         evalFnc,
			Weight:          a.Weight + InArrayWeight*len(eb),
			isDeterministic: isDc,
		}, nil
	}

	ea, eb := a.Values, b.EvalFnc

	evalFnc := func(ctx *Context) bool {
		va, vb := ea, eb(ctx)
		res, vm := arrayOp(va, vb)
		if res {
			ctx.AddMatchingSubExpr(MatchingValue{Field: a.Field, Value: va, Offset: a.Offset}, MatchingValue{Field: b.Field, Value: vm, Offset: b.Offset})
		}
		return res
	}

	return &BoolEvaluator{
		EvalFnc:         evalFnc,
		Weight:          b.Weight,
		isDeterministic: isDc,
	}, nil
}

// ArrayBoolContains evaluates array of bool against a value
func ArrayBoolContains(a *BoolEvaluator, b *BoolArrayEvaluator, state *State) (*BoolEvaluator, error) {
	isDc := isArithmDeterministic(a, b, state)

	if a.Field != "" {
		for _, value := range b.Values {
			if err := state.UpdateFieldValues(a.Field, FieldValue{Value: value}); err != nil {
				return nil, err
			}
		}
	}

	if b.Field != "" {
		if err := state.UpdateFieldValues(b.Field, FieldValue{Value: a.Value}); err != nil {
			return nil, err
		}
	}

	arrayOp := func(a bool, b []bool) (bool, bool) {
		for _, v := range b {
			if v == a {
				return true, v
			}
		}
		return false, false
	}
	if a.EvalFnc != nil && b.EvalFnc != nil {
		ea, eb := a.EvalFnc, b.EvalFnc

		evalFnc := func(ctx *Context) bool {
			va, vb := ea(ctx), eb(ctx)
			res, vm := arrayOp(va, vb)
			if res {
				ctx.AddMatchingSubExpr(MatchingValue{Field: a.Field, Value: va, Offset: a.Offset}, MatchingValue{Value: vm, Offset: b.Offset})
			}
			return res
		}

		return &BoolEvaluator{
			EvalFnc:         evalFnc,
			Weight:          a.Weight + b.Weight,
			isDeterministic: isDc,
		}, nil
	}

	if a.EvalFnc == nil && b.EvalFnc == nil {
		ea, eb := a.Value, b.Values
		res, _ := arrayOp(ea, eb)

		return &BoolEvaluator{
			Value:           res,
			Weight:          a.Weight + InArrayWeight*len(eb),
			isDeterministic: isDc,
		}, nil
	}

	if a.EvalFnc != nil {
		ea, eb := a.EvalFnc, b.Values

		evalFnc := func(ctx *Context) bool {
			va, vb := ea(ctx), eb
			res, vm := arrayOp(va, vb)
			if res {
				ctx.AddMatchingSubExpr(MatchingValue{Field: a.Field, Value: va, Offset: a.Offset}, MatchingValue{Value: vm, Offset: b.Offset})
			}
			return res
		}

		return &BoolEvaluator{
			EvalFnc:         evalFnc,
			Weight:          a.Weight + InArrayWeight*len(eb),
			isDeterministic: isDc,
		}, nil
	}

	ea, eb := a.Value, b.EvalFnc

	evalFnc := func(ctx *Context) bool {
		va, vb := ea, eb(ctx)
		res, vm := arrayOp(va, vb)
		if res {
			ctx.AddMatchingSubExpr(MatchingValue{Field: a.Field, Value: va, Offset: a.Offset}, MatchingValue{Field: b.Field, Value: vm, Offset: b.Offset})
		}
		return res
	}

	return &BoolEvaluator{
		EvalFnc:         evalFnc,
		Weight:          b.Weight,
		isDeterministic: isDc,
	}, nil
}

// CIDREquals evaluates CIDR ranges
func CIDREquals(a *CIDREvaluator, b *CIDREvaluator, state *State) (*BoolEvaluator, error) {
	isDc := isArithmDeterministic(a, b, state)

	if a.Field != "" {
		if err := state.UpdateFieldValues(a.Field, FieldValue{Value: b.Value, Type: b.ValueType}); err != nil {
			return nil, err
		}
	}

	if b.Field != "" {
		if err := state.UpdateFieldValues(b.Field, FieldValue{Value: a.Value, Type: a.ValueType}); err != nil {
			return nil, err
		}
	}

	if a.EvalFnc != nil && b.EvalFnc != nil {
		ea, eb := a.EvalFnc, b.EvalFnc

		evalFnc := func(ctx *Context) bool {
			va, vb := ea(ctx), eb(ctx)
			res := IPNetsMatch(&va, &vb)
			if res {
				ctx.AddMatchingSubExpr(MatchingValue{Field: a.Field, Value: va, Offset: a.Offset}, MatchingValue{Field: b.Field, Value: vb, Offset: b.Offset})
			}
			return res
		}

		return &BoolEvaluator{
			EvalFnc:         evalFnc,
			Weight:          a.Weight + b.Weight,
			isDeterministic: isDc,
		}, nil
	}

	if a.EvalFnc == nil && b.EvalFnc == nil {
		ea, eb := a.Value, b.Value

		return &BoolEvaluator{
			Value:           IPNetsMatch(&ea, &eb),
			Weight:          a.Weight + b.Weight,
			isDeterministic: isDc,
		}, nil
	}

	if a.EvalFnc != nil {
		ea, eb := a.EvalFnc, b.Value

		evalFnc := func(ctx *Context) bool {
			va, vb := ea(ctx), eb
			res := IPNetsMatch(&va, &vb)
			if res {
				ctx.AddMatchingSubExpr(MatchingValue{Field: a.Field, Value: va, Offset: a.Offset}, MatchingValue{Value: vb, Offset: b.Offset})
			}
			return res
		}

		return &BoolEvaluator{
			EvalFnc:         evalFnc,
			Weight:          a.Weight + b.Weight,
			isDeterministic: isDc,
		}, nil
	}

	ea, eb := a.Value, b.EvalFnc

	evalFnc := func(ctx *Context) bool {
		va, vb := ea, eb(ctx)
		res := IPNetsMatch(&va, &vb)
		if res {
			ctx.AddMatchingSubExpr(MatchingValue{Value: va, Offset: a.Offset}, MatchingValue{Field: b.Field, Value: vb, Offset: b.Offset})
		}
		return res
	}

	return &BoolEvaluator{
		EvalFnc:         evalFnc,
		Weight:          b.Weight,
		isDeterministic: isDc,
	}, nil
}

// CIDRValuesContains evaluates a CIDR against a list of CIDRs
func CIDRValuesContains(a *CIDREvaluator, b *CIDRValuesEvaluator, state *State) (*BoolEvaluator, error) {
	isDc := isArithmDeterministic(a, b, state)

	if a.Field != "" {
		for _, value := range b.Value.fieldValues {
			if err := state.UpdateFieldValues(a.Field, value); err != nil {
				return nil, err
			}
		}
	}

	if a.EvalFnc != nil && b.EvalFnc != nil {
		ea, eb := a.EvalFnc, b.EvalFnc

		evalFnc := func(ctx *Context) bool {
			va, vb := ea(ctx), eb(ctx)
			res, vm := vb.Contains(&va)
			if res {
				ctx.AddMatchingSubExpr(MatchingValue{Field: a.Field, Value: va, Offset: a.Offset}, MatchingValue{Value: vm, Offset: b.Offset})
			}

			return res
		}

		return &BoolEvaluator{
			EvalFnc:         evalFnc,
			Weight:          a.Weight + b.Weight,
			isDeterministic: isDc,
		}, nil
	}

	if a.EvalFnc == nil && b.EvalFnc == nil {
		ea, eb := a.Value, b.Value
		res, _ := eb.Contains(&ea)

		return &BoolEvaluator{
			Value:           res,
			Weight:          a.Weight + InArrayWeight*len(eb.ipnets),
			isDeterministic: isDc,
		}, nil
	}

	if a.EvalFnc != nil {
		ea, eb := a.EvalFnc, b.Value

		evalFnc := func(ctx *Context) bool {
			va, vb := ea(ctx), eb
			res, vm := vb.Contains(&va)
			if res {
				ctx.AddMatchingSubExpr(MatchingValue{Field: a.Field, Value: va, Offset: a.Offset}, MatchingValue{Value: vm, Offset: b.Offset})
			}
			return res
		}

		return &BoolEvaluator{
			EvalFnc:         evalFnc,
			Weight:          a.Weight + InArrayWeight*len(eb.fieldValues),
			isDeterministic: isDc,
		}, nil
	}

	ea, eb := a.Value, b.EvalFnc

	evalFnc := func(ctx *Context) bool {
		res, _ := eb(ctx).Contains(&ea)
		return res
	}

	return &BoolEvaluator{
		EvalFnc:         evalFnc,
		Weight:          b.Weight,
		isDeterministic: isDc,
	}, nil
}

func cidrArrayMatches(a *CIDRArrayEvaluator, b *CIDRValuesEvaluator, state *State, arrayOp func(a []net.IPNet, b *CIDRValues) (bool, interface{})) (*BoolEvaluator, error) {
	isDc := isArithmDeterministic(a, b, state)

	if a.Field != "" {
		for _, value := range b.Value.fieldValues {
			if err := state.UpdateFieldValues(a.Field, value); err != nil {
				return nil, err
			}
		}
	}

	if a.EvalFnc != nil && b.EvalFnc != nil {
		ea, eb := a.EvalFnc, b.EvalFnc

		evalFnc := func(ctx *Context) bool {
			va, vb := ea(ctx), eb(ctx)
			res, vm := arrayOp(va, vb)
			if res {
				ctx.AddMatchingSubExpr(MatchingValue{Field: a.Field, Value: va, Offset: a.Offset}, MatchingValue{Value: vm, Offset: b.Offset})
			}
			return res
		}

		return &BoolEvaluator{
			EvalFnc:         evalFnc,
			Weight:          a.Weight + b.Weight,
			isDeterministic: isDc,
		}, nil
	}

	if a.EvalFnc == nil && b.EvalFnc == nil {
		ea, eb := a.Value, b.Value
		res, _ := arrayOp(ea, &eb)

		return &BoolEvaluator{
			Value:           res,
			Weight:          a.Weight + InArrayWeight*len(eb.fieldValues),
			isDeterministic: isDc,
		}, nil
	}

	if a.EvalFnc != nil {
		ea, eb := a.EvalFnc, b.Value

		evalFnc := func(ctx *Context) bool {
			va, vb := ea(ctx), eb
			res, vm := arrayOp(va, &vb)
			if res {
				ctx.AddMatchingSubExpr(MatchingValue{Field: a.Field, Value: va, Offset: a.Offset}, MatchingValue{Value: vm, Offset: b.Offset})
			}
			return res
		}

		return &BoolEvaluator{
			EvalFnc:         evalFnc,
			Weight:          a.Weight + InArrayWeight*len(eb.fieldValues),
			isDeterministic: isDc,
		}, nil
	}

	ea, eb := a.Value, b.EvalFnc

	evalFnc := func(ctx *Context) bool {
		res, _ := arrayOp(ea, eb(ctx))
		return res
	}

	return &BoolEvaluator{
		EvalFnc:         evalFnc,
		Weight:          b.Weight,
		isDeterministic: isDc,
	}, nil
}

// CIDRArrayMatches weak comparison, at least one element of a should be in b.
func CIDRArrayMatches(a *CIDRArrayEvaluator, b *CIDRValuesEvaluator, state *State) (*BoolEvaluator, error) {
	op := func(ipnets []net.IPNet, values *CIDRValues) (bool, interface{}) {
		return values.Match(ipnets)
	}
	return cidrArrayMatches(a, b, state, op)
}

func cidrArrayMatchesCIDREvaluator(a *CIDRArrayEvaluator, b *CIDREvaluator, state *State, arrayOp func(a []net.IPNet, b net.IPNet) bool) (*BoolEvaluator, error) {
	isDc := isArithmDeterministic(a, b, state)

	if a.EvalFnc != nil && b.EvalFnc != nil {
		ea, eb := a.EvalFnc, b.EvalFnc

		evalFnc := func(ctx *Context) bool {
			return arrayOp(ea(ctx), eb(ctx))
		}

		return &BoolEvaluator{
			EvalFnc:         evalFnc,
			Weight:          a.Weight + b.Weight,
			isDeterministic: isDc,
		}, nil
	}

	if a.EvalFnc == nil && b.EvalFnc == nil {
		ea, eb := a.Value, b.Value

		return &BoolEvaluator{
			Value:           arrayOp(ea, eb),
			Weight:          b.Weight + a.Weight,
			isDeterministic: isDc,
		}, nil
	}

	if a.EvalFnc != nil {
		ea, eb := a.EvalFnc, b.Value

		evalFnc := func(ctx *Context) bool {
			return arrayOp(ea(ctx), eb)
		}

		return &BoolEvaluator{
			EvalFnc:         evalFnc,
			Weight:          a.Weight,
			isDeterministic: isDc,
		}, nil
	}

	ea, eb := a.Value, b.EvalFnc

	evalFnc := func(ctx *Context) bool {
		return arrayOp(ea, eb(ctx))
	}

	return &BoolEvaluator{
		EvalFnc:         evalFnc,
		Weight:          b.Weight,
		isDeterministic: isDc,
	}, nil
}

// CIDRArrayMatchesCIDREvaluator weak comparison, at least one element of a should be in b.
func CIDRArrayMatchesCIDREvaluator(a *CIDRArrayEvaluator, b *CIDREvaluator, state *State) (*BoolEvaluator, error) {
	op := func(values []net.IPNet, ipnet net.IPNet) bool {
		for _, ip := range values {
			if ip.Contains(ipnet.IP) {
				return true
			}
		}
		return false
	}
	return cidrArrayMatchesCIDREvaluator(a, b, state, op)
}

// CIDRArrayMatchesAll ensures that all values from a and b match.
func CIDRArrayMatchesAll(a *CIDRArrayEvaluator, b *CIDRValuesEvaluator, state *State) (*BoolEvaluator, error) {
	op := func(ipnets []net.IPNet, values *CIDRValues) (bool, interface{}) {
		return values.MatchAll(ipnets)
	}
	return cidrArrayMatches(a, b, state, op)
}

// CIDRArrayContains evaluates a CIDR against a list of CIDRs
func CIDRArrayContains(a *CIDREvaluator, b *CIDRArrayEvaluator, state *State) (*BoolEvaluator, error) {
	isDc := isArithmDeterministic(a, b, state)

	if a.Field != "" {
		for _, value := range b.Value {
			if err := state.UpdateFieldValues(a.Field, FieldValue{Type: IPNetValueType, Value: value}); err != nil {
				return nil, err
			}
		}
	}

	arrayOp := func(a *net.IPNet, b []net.IPNet) (bool, net.IPNet) {
		for _, n := range b {
			if IPNetsMatch(a, &n) {
				return true, n
			}
		}
		return false, net.IPNet{}
	}

	if a.EvalFnc != nil && b.EvalFnc != nil {
		ea, eb := a.EvalFnc, b.EvalFnc

		evalFnc := func(ctx *Context) bool {
			va, vb := ea(ctx), eb(ctx)
			res, vm := arrayOp(&va, vb)
			if res {
				ctx.AddMatchingSubExpr(MatchingValue{Field: a.Field, Value: va, Offset: a.Offset}, MatchingValue{Field: b.Field, Value: vm, Offset: b.Offset})
			}
			return res
		}

		return &BoolEvaluator{
			EvalFnc:         evalFnc,
			Weight:          a.Weight + b.Weight,
			isDeterministic: isDc,
		}, nil
	}

	if a.EvalFnc == nil && b.EvalFnc == nil {
		ea, eb := a.Value, b.Value
		res, _ := arrayOp(&ea, eb)

		return &BoolEvaluator{
			Value:           res,
			Weight:          a.Weight + InArrayWeight*len(eb),
			isDeterministic: isDc,
		}, nil
	}

	if a.EvalFnc != nil {
		ea, eb := a.EvalFnc, b.Value

		evalFnc := func(ctx *Context) bool {
			va, vb := ea(ctx), eb
			res, vm := arrayOp(&va, vb)
			if res {
				ctx.AddMatchingSubExpr(MatchingValue{Field: a.Field, Value: va, Offset: a.Offset}, MatchingValue{Value: vm, Offset: b.Offset})
			}
			return res
		}

		return &BoolEvaluator{
			EvalFnc:         evalFnc,
			Weight:          a.Weight + InArrayWeight*len(eb),
			isDeterministic: isDc,
		}, nil
	}

	ea, eb := a.Value, b.EvalFnc

	evalFnc := func(ctx *Context) bool {
		res, _ := arrayOp(&ea, eb(ctx))
		return res
	}

	return &BoolEvaluator{
		EvalFnc:         evalFnc,
		Weight:          b.Weight,
		isDeterministic: isDc,
	}, nil
}
