package statmodel

import (
	"fmt"
	"math"
	"math/rand"
	"strings"

	"github.com/brookluers/dstream/dstream"
	"gonum.org/v1/gonum/floats"
	"gonum.org/v1/gonum/mat"
)

// Knockoff is a dstream that creates knockoff versions of variables
// from another dstream.  The specific approach implemented here is
// the "equivariant knockoff" of Barber and Candes (2014).  As with
// any other dstream transform, do not retain the source dstream after
// creating a knockoff from it.
type Knockoff struct {
	data dstream.Dstream

	// The variable names for the knockoff stream (original
	// variables and their knockoff counterparts are included).
	names []string

	// The numbver of variables in the source data
	nvarSource int

	// Map from variable names to column position
	vpos map[string]int

	// Positions of the variables in the source data to knockoff
	pos []int

	// The means of the variables being knocked-off.
	mean []float64

	// The L2-norms of the variables being knocked-off.
	scale []float64

	// The knockoff features are X*rmat + U*cmat where X are the
	// actual features and U are orthogonal to X.
	rmat []float64
	cmat []float64

	// The cross product matrix of the features
	cpr []float64

	// The minimum eigenvalue of cpr
	lmin float64

	// The sample size per slice and total sample size
	nobs []int
	ntot int

	// The current chunk index
	chunk int

	// The current data arrays.
	bdat [][]float64
}

// NewKnockoff creates a knockoff data stream from the given source
// data stream. A knockoff variable is constructed for each variable
// in 'kvars'.  All knockoff variables (both the original and the
// knockoff version of the variable) are standardized.  Variables not
// listed in kvars are retained but are not standardized or otherwise
// altered.  The returned Knockoff struct value satisfies the dstream
// interface.
func NewKnockoff(data dstream.Dstream, kvars []string) *Knockoff {

	// Map from variable names to column position.
	mp := make(map[string]int)
	for k, v := range data.Names() {
		mp[v] = k
	}

	// Get the positions of the features to be analyzed via
	// knockoff.
	var pos []int
	for _, na := range kvars {
		q, ok := mp[na]
		if !ok {
			msg := fmt.Sprintf("Variable '%s' not found\n", na)
			panic(msg)
		}
		pos = append(pos, q)
	}

	ko := &Knockoff{
		data:       data,
		pos:        pos,
		nvarSource: data.NumVar(),
		bdat:       make([][]float64, len(pos)),
		chunk:      -1,
	}

	ko.init()
	return ko
}

func (ko *Knockoff) init() {
	ko.getMoments()
	ko.getCrossProd()
	ko.getlmin()
	ko.setrcmat()
	ko.setNames()
}

func (ko *Knockoff) CrossProd() []float64 {
	return ko.cpr
}

func (ko *Knockoff) CrossProdMinEig() float64 {
	return ko.lmin
}

// getMoments calculates the means and L2 norms of the knockoff
// variables.
func (ko *Knockoff) getMoments() {

	p := len(ko.pos)

	// Get the means of the knockoff variables.
	n := 0
	ko.mean = make([]float64, p)
	ko.data.Reset()
	for ko.data.Next() {
		for i, j := range ko.pos {
			x := ko.data.GetPos(j).([]float64)
			ko.mean[i] += floats.Sum(x)
			if i == 0 {
				n += len(x)
			}
		}
	}
	for j := range ko.mean {
		ko.mean[j] /= float64(n)
	}

	// Get the L2 norms of the knockoff variables
	ko.scale = make([]float64, p)
	ko.data.Reset()
	for ko.data.Next() {
		for i, j := range ko.pos {
			x := ko.data.GetPos(j).([]float64)
			for k := range x {
				u := x[k] - ko.mean[i]
				ko.scale[i] += u * u
			}
		}
	}
	for j := range ko.scale {
		ko.scale[j] = math.Sqrt(ko.scale[j])
	}

	// Check for errors
	for j, s := range ko.scale {
		if s == 0 || math.IsNaN(s) || math.IsInf(s, 0) {
			msg := fmt.Sprintf("Variable '%s' has zero variance or Inf/NaN values.",
				ko.data.Names()[ko.pos[j]])
			panic(msg)
		}
	}
}

// Get the cross product matrix, pooling over all chunks.
func (ko *Knockoff) getCrossProd() {

	p := len(ko.pos)
	cpr := make([]float64, p*p)
	ko.nobs = ko.nobs[0:0]

	ko.data.Reset()
	for ko.data.Next() {

		// Get the variables for this chunk
		var vax [][]float64
		for _, j := range ko.pos {
			vax = append(vax, ko.data.GetPos(j).([]float64))
		}

		ko.nobs = append(ko.nobs, len(vax[0]))
		ko.ntot += len(vax[0])

		// Update the cross products of the knockoff variables
		for j1 := 0; j1 < p; j1++ {
			for j2 := 0; j2 <= j1; j2++ {

				n := len(vax[0])
				s := 0.0
				for k := 0; k < n; k++ {
					u1 := (vax[j1][k] - ko.mean[j1]) / ko.scale[j1]
					u2 := (vax[j2][k] - ko.mean[j2]) / ko.scale[j2]
					s += u1 * u2
				}

				cpr[j1*p+j2] += s
				if j1 != j2 {
					cpr[j2*p+j1] += s
				}
			}
		}
	}

	// Check for errors
	for _, v := range cpr {
		if math.IsNaN(v) {
			msg := "Cross product matrix has NaN values.\n"
			panic(msg)
		}
	}

	ko.cpr = cpr
}

// Get the minimum eigenvalue of the cross product matrix.
func (ko *Knockoff) getlmin() {

	p := len(ko.pos)
	es := new(mat.EigenSym)
	ok := es.Factorize(mat.NewSymDense(p, ko.cpr), false)
	if !ok {
		panic("Can't factorize the cross product matrix, it may not be PSD.\n")
	}

	va := es.Values(nil)
	ko.lmin = floats.Min(va)
}

// Construct rmat and cmat.  The knockoff features are X*rmat + U*cmat
// where X are the actual features and U are orthogonal to X.
func (ko *Knockoff) setrcmat() {

	p := len(ko.pos)

	// Inverse of the cross product matrix
	ma := mat.NewDense(p, p, ko.cpr)
	mi := mat.NewDense(p, p, nil)
	err := mi.Inverse(ma)
	if err != nil {
		print("Can't invert cross product matrix\n")
		panic(err)
	}

	f := 2 * ko.lmin
	if f > 1 {
		f = 1
	}

	ko.rmat = make([]float64, p*p)
	for i := 0; i < p; i++ {
		for j := 0; j < p; j++ {
			ko.rmat[i*p+j] = -f * mi.At(i, j)
		}
		ko.rmat[i*p+i] += 1
	}

	// Barber and Candes equation 2.2
	am := mat.NewSymDense(p, nil)
	for i := 0; i < p; i++ {
		for j := 0; j <= i; j++ {
			am.SetSym(i, j, -f*f*mi.At(i, j))
		}
		am.SetSym(i, i, am.At(i, i)+2*f)
	}
	es := new(mat.EigenSym)
	if !es.Factorize(am, true) {
		panic("EigenSym!\n")
	}
	lmat := new(mat.Dense)
	lmat.EigenvectorsSym(es)
	va := es.Values(nil)
	// Clip small negative eigenvalues
	for j := range va {
		if math.Abs(va[j]) < 1e-10 && va[j] < 0 {
			va[j] = 0
		}
	}
	if floats.Min(va) < 0 {
		panic("A matrix has negative eigenvalues\n")
	}
	ko.cmat = make([]float64, p*p)
	for i := 0; i < p; i++ {
		for j := 0; j < p; j++ {
			ko.cmat[j*p+i] = lmat.At(i, j) * math.Sqrt(va[j])
		}
	}

	for _, v := range ko.rmat {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			msg := "R matrix has Inf or NaN values.\n"
			panic(msg)
		}
	}

	for _, v := range ko.cmat {
		if math.IsNaN(v) || math.IsInf(v, 0) {
			msg := "C matrix has Inf or NaN values.\n"
			panic(msg)
		}
	}
}

func (ko *Knockoff) Names() []string {
	return ko.names
}

func (ko *Knockoff) Reset() {
	ko.chunk = -1
	ko.data.Reset()
}

func (ko *Knockoff) setNames() {

	na := ko.data.Names()
	var nak []string

	for _, j := range ko.pos {
		nak = append(nak, na[j]+"_ko")
	}

	ko.names = append(ko.names, na...)
	ko.names = append(ko.names, nak...)

	vpos := make(map[string]int)
	for k, v := range nak {
		vpos[v] = k
	}
	ko.vpos = vpos
}

func (ko *Knockoff) GetPos(j int) interface{} {
	p := ko.nvarSource
	if j < p {
		// An original variable
		z := ko.data.GetPos(j).([]float64)
		y := make([]float64, len(z))
		copy(y, z)

		// Standardize the variables that will be knocked off.
		m := 0.0
		s := 1.0
		f := false
		for i, k := range ko.pos {
			if j == k {
				m = ko.mean[i]
				s = ko.scale[i]
				f = true
			}
		}
		if f {
			floats.AddConst(-m, y)
			floats.Scale(1/s, y)
		}

		return y
	} else {
		// A knockoff variable
		return ko.bdat[j-p]
	}
}

func (ko *Knockoff) Get(name string) interface{} {
	pos, ok := ko.vpos[name]
	if !ok {
		msg := fmt.Sprintf("Variable '%s' not found\n", name)
		panic(msg)
	}
	return ko.GetPos(pos)
}

// orthog returns an orthogonal matrix whose columns are orthogonal to
// the columns of ma.
func (ko *Knockoff) orthog(ma *mat.Dense) *mat.Dense {

	n, p := ma.Dims()
	if n < 2*(p-1)+1 {
		panic("Knockoff requires n >= 2*p+1\n")
	}

	// Orthogonalize ma.
	sv := new(mat.SVD)
	if !sv.Factorize(ma, mat.SVDThin) {
		panic("SVD!\n")
	}
	maq := sv.UTo(nil)

	// Start with a matrix of random values
	mr := mat.NewDense(n, p-1, nil)
	for i := 0; i < n; i++ {
		for j := 0; j < p-1; j++ {
			mr.Set(i, j, rand.NormFloat64())
		}
	}

	// Project away from col(ma) = col(maq)
	qm := mat.NewDense(p, p-1, nil)
	qm.Mul(maq.T(), mr)
	fm := mat.NewDense(n, p-1, nil)
	fm.Mul(maq, qm)

	md := mat.NewDense(n, p-1, nil)
	md.Sub(mr, fm)

	// Orthogonalize
	sv = new(mat.SVD)
	if !sv.Factorize(md, mat.SVDThin) {
		panic("SVD!\n")
	}
	u := sv.UTo(nil)

	f := math.Sqrt(float64(ko.nobs[ko.chunk]) / float64(ko.ntot))
	u.Scale(f, u)
	return u
}

func (ko *Knockoff) Next() bool {

	ko.chunk++
	if !ko.data.Next() {
		return false
	}

	var vars [][]float64
	for _, j := range ko.pos {
		vars = append(vars, ko.data.GetPos(j).([]float64))
	}

	// Put the source data into a contiguous array
	n := len(vars[0])
	p := len(vars)
	xm := make([]float64, n*(p+1))
	for i := 0; i < n; i++ {
		for j := 0; j < p; j++ {
			xm[i*(p+1)+j] = (vars[j][i] - ko.mean[j]) / ko.scale[j]
		}
		xm[i*(p+1)+p] = 1
	}
	xma := mat.NewDense(n, p+1, xm)

	um := ko.orthog(xma)

	for j := 0; j < p; j++ {

		u := make([]float64, n)
		for i := 0; i < n; i++ {
			for k := 0; k < p; k++ {
				a := (vars[k][i] - ko.mean[k]) / ko.scale[k]
				u[i] += a * ko.rmat[k*p+j]
				u[i] += um.At(i, k) * ko.cmat[k*p+j]
			}
		}

		ko.bdat[j] = u
	}

	return true
}

func (ko *Knockoff) Close() {
	ko.data.Close()
}

func (ko *Knockoff) NumObs() int {
	return ko.data.NumObs()
}

func (ko *Knockoff) NumVar() int {
	return len(ko.pos) + ko.nvarSource
}

// KnockoffResult contains the results of fitting a regression model
// using the knockoff method.
type KnockoffResult struct {

	// The names of the variables (one name for each
	// actual/knockoff variable pair)
	names []string

	// The coefficicents for the actual variables
	params []float64

	// The knockoff statisitcs
	wstat []float64

	// Indicator that the Knockoff+ method was used for FDR calculation
	plus bool

	// The calculated FDR values
	fdr []float64
}

// Names returns the names of the variables in the knockoff analysis.
// For each original/knockoff variable pair, only the original name is
// included.
func (kr *KnockoffResult) Names() []string {
	return kr.names
}

// Parms returns the estimated coefficients for the non-knockoff
// variables in the regression model.
func (kr *KnockoffResult) Params() []float64 {
	return kr.params
}

// Stat returns the knockoff statistic values for the variables in the
// regression model.  These statistics are obtained by comparing the
// coefficient for an actual variable to the coefficient for its
// knockoff counterpart, so one number is returned for each
// actual/knockoff pair of variables.
func (kr *KnockoffResult) Stat() []float64 {
	return kr.wstat
}

// FDR returns the estimated false discovery rate values for the
// variables in the regression model.
func (kr *KnockoffResult) FDR() []float64 {
	return kr.fdr
}

// Create a knockoff result from a fitted regression model that has
// been fit to a Kknockoff dataset.
func NewKnockoffResult(result BaseResultser, plus bool) *KnockoffResult {

	names := result.Names()
	params := result.Params()

	// Map from variable name to position.
	mp := make(map[string]int)
	for k, v := range names {
		mp[v] = k
	}

	// Get the names and statistics.
	var pn []string
	var wstat, rstat []float64
	for k, v := range names {

		if strings.HasSuffix(v, "_ko") {
			continue
		}
		pos, ok := mp[v+"_ko"]
		if !ok {
			continue
		}

		pn = append(pn, v)
		wstat = append(wstat, math.Abs(params[k])-math.Abs(params[pos]))
		rstat = append(rstat, params[k])
	}

	// Sort by statistic value
	ii := make([]int, len(pn))
	floats.Argsort(wstat, ii)
	var knames []string
	var rstatx []float64
	for _, i := range ii {
		// Sort knames and rstat like wstat.
		knames = append(knames, pn[i])
		rstatx = append(rstatx, rstat[i])
	}

	// Get the FDR values
	var fdr []float64
	for k := range wstat {
		denom := float64(len(wstat) - k)
		numer := 0.0
		for _, w := range wstat {
			if w <= -wstat[k] {
				numer++
			}
		}
		if plus {
			numer++
		}
		fdr = append(fdr, numer/denom)
	}

	return &KnockoffResult{
		names:  knames,
		params: rstatx,
		wstat:  wstat,
		plus:   plus,
		fdr:    fdr,
	}
}
