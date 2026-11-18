package docstore

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"time"

	"google.golang.org/protobuf/types/known/timestamppb"
)

// Documents are stored as JSON with just enough tagging to give back the same
// Go types Firestore's Data() returns: int64 for integers, float64 for
// doubles (always written with a '.' or exponent so they never come back as
// integers), time.Time as {"$t": RFC3339Nano}, []byte as {"$b": base64}.

type jsonFloat float64

func (f jsonFloat) MarshalJSON() ([]byte, error) {
	v := float64(f)
	switch {
	case math.IsNaN(v):
		return []byte(`{"$f":"NaN"}`), nil
	case math.IsInf(v, 1):
		return []byte(`{"$f":"+Inf"}`), nil
	case math.IsInf(v, -1):
		return []byte(`{"$f":"-Inf"}`), nil
	}
	s := strconv.FormatFloat(v, 'g', -1, 64)
	if !strings.ContainsAny(s, ".eE") {
		s += ".0"
	}
	return []byte(s), nil
}

// normalize converts a value a caller passes to Set/Update into the canonical
// Firestore value types: nil, bool, int64, float64, string, []byte, time.Time,
// []interface{}, map[string]interface{}.
func normalize(v interface{}) (interface{}, error) {
	switch x := v.(type) {
	case nil:
		return nil, nil
	case bool, int64, float64, string, time.Time:
		return x, nil
	case *time.Time:
		if x == nil {
			return nil, nil
		}
		return *x, nil
	case *timestamppb.Timestamp:
		// Firestore stores protobuf timestamps as timestamps.
		if x == nil {
			return nil, nil
		}
		return x.AsTime(), nil
	case []byte:
		return x, nil
	case int:
		return int64(x), nil
	case int8:
		return int64(x), nil
	case int16:
		return int64(x), nil
	case int32:
		return int64(x), nil
	case uint8:
		return int64(x), nil
	case uint16:
		return int64(x), nil
	case uint32:
		return int64(x), nil
	case uint64:
		return int64(x), nil
	case uint:
		return int64(x), nil
	case float32:
		return float64(x), nil
	case map[string]interface{}:
		out := make(map[string]interface{}, len(x))
		for k, e := range x {
			if e == Delete {
				out[k] = Delete
				continue
			}
			n, err := normalize(e)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", k, err)
			}
			out[k] = n
		}
		return out, nil
	case []interface{}:
		out := make([]interface{}, len(x))
		for i, e := range x {
			n, err := normalize(e)
			if err != nil {
				return nil, err
			}
			out[i] = n
		}
		return out, nil
	}
	if v == Delete {
		return Delete, nil
	}
	rv := reflect.ValueOf(v)
	switch rv.Kind() {
	case reflect.Ptr:
		if rv.IsNil() {
			return nil, nil
		}
		return normalize(rv.Elem().Interface())
	case reflect.String:
		return rv.String(), nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		return rv.Int(), nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		return int64(rv.Uint()), nil
	case reflect.Float32, reflect.Float64:
		return rv.Float(), nil
	case reflect.Bool:
		return rv.Bool(), nil
	case reflect.Slice, reflect.Array:
		if rv.Kind() == reflect.Slice && rv.IsNil() {
			return nil, nil
		}
		out := make([]interface{}, rv.Len())
		for i := 0; i < rv.Len(); i++ {
			n, err := normalize(rv.Index(i).Interface())
			if err != nil {
				return nil, err
			}
			out[i] = n
		}
		return out, nil
	case reflect.Map:
		if rv.Type().Key().Kind() != reflect.String {
			return nil, fmt.Errorf("docstore: map key %s not supported", rv.Type().Key())
		}
		if rv.IsNil() {
			return nil, nil
		}
		out := make(map[string]interface{}, rv.Len())
		it := rv.MapRange()
		for it.Next() {
			n, err := normalize(it.Value().Interface())
			if err != nil {
				return nil, err
			}
			out[it.Key().String()] = n
		}
		return out, nil
	case reflect.Struct:
		return normalizeStruct(rv)
	}
	return nil, fmt.Errorf("docstore: unsupported value type %T", v)
}

// normalizeStruct encodes a struct as Firestore's client does: exported
// fields by Go name, or the name in a `firestore:"name,omitempty"` tag; "-"
// skips a field.
func normalizeStruct(rv reflect.Value) (interface{}, error) {
	out := map[string]interface{}{}
	t := rv.Type()
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if !f.IsExported() {
			continue
		}
		name := f.Name
		omitEmpty := false
		if tag, ok := f.Tag.Lookup("firestore"); ok {
			parts := strings.Split(tag, ",")
			if parts[0] == "-" {
				continue
			}
			if parts[0] != "" {
				name = parts[0]
			}
			for _, o := range parts[1:] {
				if o == "omitempty" {
					omitEmpty = true
				}
			}
		}
		fv := rv.Field(i)
		if omitEmpty && fv.IsZero() {
			continue
		}
		n, err := normalize(fv.Interface())
		if err != nil {
			return nil, fmt.Errorf("%s: %w", name, err)
		}
		out[name] = n
	}
	return out, nil
}

// toJSONTree turns a normalized value into something encoding/json writes
// in the tagged form.
func toJSONTree(v interface{}) interface{} {
	switch x := v.(type) {
	case float64:
		return jsonFloat(x)
	case time.Time:
		return map[string]interface{}{"$t": x.UTC().Format(time.RFC3339Nano)}
	case []byte:
		return map[string]interface{}{"$b": base64.StdEncoding.EncodeToString(x)}
	case map[string]interface{}:
		out := make(map[string]interface{}, len(x))
		for k, e := range x {
			out[k] = toJSONTree(e)
		}
		return out
	case []interface{}:
		out := make([]interface{}, len(x))
		for i, e := range x {
			out[i] = toJSONTree(e)
		}
		return out
	}
	return v
}

func encodeDoc(m map[string]interface{}) (string, error) {
	b, err := json.Marshal(toJSONTree(m))
	if err != nil {
		return "", err
	}
	return string(b), nil
}

func decodeDoc(s string) (map[string]interface{}, error) {
	dec := json.NewDecoder(bytes.NewReader([]byte(s)))
	dec.UseNumber()
	var raw interface{}
	if err := dec.Decode(&raw); err != nil {
		return nil, err
	}
	v, err := fromJSONTree(raw)
	if err != nil {
		return nil, err
	}
	m, _ := v.(map[string]interface{})
	if m == nil {
		m = map[string]interface{}{}
	}
	return m, nil
}

func fromJSONTree(v interface{}) (interface{}, error) {
	switch x := v.(type) {
	case json.Number:
		s := string(x)
		if strings.ContainsAny(s, ".eE") {
			return strconv.ParseFloat(s, 64)
		}
		return strconv.ParseInt(s, 10, 64)
	case map[string]interface{}:
		if len(x) == 1 {
			if t, ok := x["$t"].(string); ok {
				return time.Parse(time.RFC3339Nano, t)
			}
			if b, ok := x["$b"].(string); ok {
				return base64.StdEncoding.DecodeString(b)
			}
			if f, ok := x["$f"].(string); ok {
				switch f {
				case "NaN":
					return math.NaN(), nil
				case "+Inf":
					return math.Inf(1), nil
				case "-Inf":
					return math.Inf(-1), nil
				}
			}
		}
		out := make(map[string]interface{}, len(x))
		for k, e := range x {
			d, err := fromJSONTree(e)
			if err != nil {
				return nil, err
			}
			out[k] = d
		}
		return out, nil
	case []interface{}:
		out := make([]interface{}, len(x))
		for i, e := range x {
			d, err := fromJSONTree(e)
			if err != nil {
				return nil, err
			}
			out[i] = d
		}
		return out, nil
	}
	return v, nil
}

// typeRank orders values of different types the way Firestore does.
func typeRank(v interface{}) int {
	switch v.(type) {
	case nil:
		return 0
	case bool:
		return 1
	case int64, float64:
		return 2
	case time.Time:
		return 3
	case string:
		return 4
	case []byte:
		return 5
	case []interface{}:
		return 7
	case map[string]interface{}:
		return 8
	}
	return 9
}

// compare returns -1, 0 or 1. Values of different types compare by type rank.
func compare(a, b interface{}) int {
	ra, rb := typeRank(a), typeRank(b)
	if ra != rb {
		if ra < rb {
			return -1
		}
		return 1
	}
	switch x := a.(type) {
	case nil:
		return 0
	case bool:
		y := b.(bool)
		switch {
		case x == y:
			return 0
		case !x:
			return -1
		}
		return 1
	case int64, float64:
		fa, fb := toFloat(a), toFloat(b)
		if ia, ok := a.(int64); ok {
			if ib, ok := b.(int64); ok {
				fa, fb = 0, 0
				switch {
				case ia < ib:
					return -1
				case ia > ib:
					return 1
				}
				return 0
			}
		}
		switch {
		case fa < fb:
			return -1
		case fa > fb:
			return 1
		}
		return 0
	case time.Time:
		y := b.(time.Time)
		switch {
		case x.Before(y):
			return -1
		case x.After(y):
			return 1
		}
		return 0
	case string:
		return strings.Compare(x, b.(string))
	case []byte:
		return bytes.Compare(x, b.([]byte))
	case []interface{}:
		y := b.([]interface{})
		for i := 0; i < len(x) && i < len(y); i++ {
			if c := compare(x[i], y[i]); c != 0 {
				return c
			}
		}
		return compareInt(len(x), len(y))
	case map[string]interface{}:
		y := b.(map[string]interface{})
		ka, kb := sortedKeys(x), sortedKeys(y)
		for i := 0; i < len(ka) && i < len(kb); i++ {
			if c := strings.Compare(ka[i], kb[i]); c != 0 {
				return c
			}
			if c := compare(x[ka[i]], y[kb[i]]); c != 0 {
				return c
			}
		}
		return compareInt(len(ka), len(kb))
	}
	return 0
}

func compareInt(a, b int) int {
	switch {
	case a < b:
		return -1
	case a > b:
		return 1
	}
	return 0
}

func toFloat(v interface{}) float64 {
	switch x := v.(type) {
	case int64:
		return float64(x)
	case float64:
		return x
	}
	return 0
}

func sortedKeys(m map[string]interface{}) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// getPath reads a dotted field path from a document.
func getPath(m map[string]interface{}, path string) (interface{}, bool) {
	var cur interface{} = m
	for _, part := range strings.Split(path, ".") {
		mm, ok := cur.(map[string]interface{})
		if !ok {
			return nil, false
		}
		cur, ok = mm[part]
		if !ok {
			return nil, false
		}
	}
	return cur, true
}

// setPath writes (or with Delete removes) a dotted field path, creating
// intermediate maps.
func setPath(m map[string]interface{}, path string, v interface{}) {
	parts := strings.Split(path, ".")
	cur := m
	for _, p := range parts[:len(parts)-1] {
		next, ok := cur[p].(map[string]interface{})
		if !ok {
			if v == Delete {
				return
			}
			next = map[string]interface{}{}
			cur[p] = next
		}
		cur = next
	}
	last := parts[len(parts)-1]
	if v == Delete {
		delete(cur, last)
		return
	}
	cur[last] = v
}

// mergeAll merges src into dst the way Set(..., MergeAll) does: nested maps
// merge leaf by leaf, everything else (including arrays) replaces.
func mergeAll(dst, src map[string]interface{}) {
	for k, v := range src {
		if v == Delete {
			delete(dst, k)
			continue
		}
		if sm, ok := v.(map[string]interface{}); ok {
			dm, ok := dst[k].(map[string]interface{})
			if !ok {
				dm = map[string]interface{}{}
				dst[k] = dm
			}
			mergeAll(dm, sm)
			continue
		}
		dst[k] = v
	}
}

// stripDeletes removes Delete sentinels from a map written without merge.
func stripDeletes(m map[string]interface{}) {
	for k, v := range m {
		if v == Delete {
			delete(m, k)
			continue
		}
		if sm, ok := v.(map[string]interface{}); ok {
			stripDeletes(sm)
		}
	}
}
