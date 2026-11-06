package app

import (
	"errors"
	"strings"

	"github.com/getkin/kin-openapi/openapi3"
	"github.com/getkin/kin-openapi/openapi3filter"

	apigen "github.com/Tramo-Labs/hq/eng/core/gen/api"
)

// badRequestDetail turns a validation or binding error into one short,
// stable line naming the parameter or body field at fault (F17). It never
// returns the library's message: that one prints the whole schema and the
// value sent, password included.
func badRequestDetail(err error) string {
	var me openapi3.MultiError
	if errors.As(err, &me) && len(me) > 0 {
		err = me[0]
	}
	var re *openapi3filter.RequestError
	if errors.As(err, &re) {
		switch {
		case re.Parameter != nil:
			return `invalid parameter "` + re.Parameter.Name + `"`
		case re.RequestBody != nil:
			return bodyDetail(re.Err)
		}
		return "invalid request"
	}
	if name := paramName(err); name != "" {
		return `invalid parameter "` + name + `"`
	}
	return "invalid request"
}

// bodyDetail names the JSON pointer of the failing field. An unknown
// property is reported as such without its name: the name was chosen by the
// caller, so it is not echoed.
func bodyDetail(err error) string {
	var me openapi3.MultiError
	if errors.As(err, &me) && len(me) > 0 {
		err = me[0]
	}
	var se *openapi3.SchemaError
	if !errors.As(err, &se) {
		return "invalid request body"
	}
	if se.SchemaField == "additionalProperties" || strings.HasSuffix(se.Reason, " is unsupported") {
		return "invalid request body: unknown property"
	}
	if p := se.JSONPointer(); len(p) > 0 {
		return "invalid request body: /" + strings.Join(p, "/")
	}
	return "invalid request body"
}

// paramName extracts the parameter of the generated router's binding
// errors (path, query and header parameters it parses itself).
func paramName(err error) string {
	var (
		f *apigen.InvalidParamFormatError
		r *apigen.RequiredParamError
		h *apigen.RequiredHeaderError
		u *apigen.UnmarshalingParamError
		m *apigen.TooManyValuesForParamError
		c *apigen.UnescapedCookieParamError
	)
	switch {
	case errors.As(err, &f):
		return f.ParamName
	case errors.As(err, &r):
		return r.ParamName
	case errors.As(err, &h):
		return h.ParamName
	case errors.As(err, &u):
		return u.ParamName
	case errors.As(err, &m):
		return m.ParamName
	case errors.As(err, &c):
		return c.ParamName
	}
	return ""
}
