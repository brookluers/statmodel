package statmodel

import (
	"testing"

	"gonum.org/v1/gonum/floats"

	"github.com/brookluers/dstream/dstream"
)

func data1() dstream.Dstream {
	y := []interface{}{
		[]float64{0, 1, 3, 2, 1, 1, 0},
	}
	x1 := []interface{}{
		[]float64{1, 1, 1, 1, 1, 1, 1},
	}
	x2 := []interface{}{
		[]float64{4, 1, -1, 3, 5, -5, 3},
	}
	dat := [][]interface{}{y, x1, x2}
	na := []string{"y", "x1", "x2"}
	da := dstream.NewFromArrays(dat, na)
	return da
}

func data2() dstream.Dstream {
	y := []interface{}{
		[]float64{0, 0, 1, 0, 1, 0, 0},
	}
	x1 := []interface{}{
		[]float64{1, 1, 1, 1, 1, 1, 1},
	}
	x2 := []interface{}{
		[]float64{4, 1, -1, 3, 5, -5, 3},
	}
	x3 := []interface{}{
		[]float64{1, -1, 1, 1, 2, 5, -1},
	}
	dat := [][]interface{}{y, x1, x2, x3}
	na := []string{"y", "x1", "x2", "x3"}
	da := dstream.NewFromArrays(dat, na)
	return da
}

func TestDims(t *testing.T) {

	da := data1()
	if da.NumObs() != 7 {
		t.Fail()
	}
	if da.NumVar() != 3 {
		t.Fail()
	}

	da = data2()
	if da.NumObs() != 7 {
		t.Fail()
	}
	if da.NumVar() != 4 {
		t.Fail()
	}
}

// A mock model for testing
type Mock struct {
	data dstream.Dstream
	xpos []int
}

func (m *Mock) DataSet() dstream.Dstream {
	return m.data
}

func (m *Mock) LogLike(params Parameter) float64 {
	return 0
}

func (m *Mock) Score(params Parameter, score []float64) {
}

func (m *Mock) Hessian(params Parameter, ht HessType, score []float64) {
}

func (m *Mock) NumParams() int {
	return m.data.NumVar() - 1
}

func (m *Mock) Xpos() []int {
	return m.xpos
}

func TestResult1(t *testing.T) {

	da := data1()
	model := &Mock{
		da,
		[]int{1, 2},
	}

	params := []float64{1, 2}
	xnames := []string{"x1", "x2"}
	vcov := []float64{0, 0, 0, 0}

	r := NewBaseResults(model, 0, params, xnames, vcov)

	// Test fitted values on the training data.
	fv := []float64{9, 3, -1, 7, 11, -9, 7}
	if !floats.Equal(fv, r.FittedValues(nil)) {
		t.Fail()
	}

	f := func(x interface{}) {
		z := x.([]float64)
		for i, _ := range z {
			z[i] = 2 * z[i]
		}
	}

	// Test fitted values when passing a new data stream.
	da = dstream.Mutate(da, "x2", f)
	fv = []float64{17, 5, -3, 13, 21, -19, 13}
	if !floats.Equal(fv, r.FittedValues(da)) {
		t.Fail()
	}
}
