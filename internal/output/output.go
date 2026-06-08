// internal/output/output.go
package output

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"reflect"
	"strings"
	"text/tabwriter"
)

// Fprint writes v as JSON (pretty=false) or a human table (pretty=true).
func Fprint(w io.Writer, pretty bool, v any) error {
	if pretty {
		return fprintTable(w, v)
	}
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

// Print writes to os.Stdout.
func Print(pretty bool, v any) error {
	return Fprint(os.Stdout, pretty, v)
}

// Err writes an error message to stderr.
func Err(format string, args ...any) {
	fmt.Fprintf(os.Stderr, "error: "+format+"\n", args...)
}

func fprintTable(w io.Writer, v any) error {
	rv := reflect.ValueOf(v)
	if rv.Kind() == reflect.Ptr {
		rv = rv.Elem()
	}
	if rv.Kind() != reflect.Slice {
		// Single value: print as key: value pairs
		data, err := json.MarshalIndent(v, "", "  ")
		if err != nil {
			return err
		}
		var m map[string]any
		if err := json.Unmarshal(data, &m); err != nil {
			_, werr := fmt.Fprintln(w, string(data))
			return werr
		}
		tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
		for k, val := range m {
			fmt.Fprintf(tw, "%s\t%v\n", k, val)
		}
		return tw.Flush()
	}

	// Slice: print as table with header row from JSON keys
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	var rows []map[string]any
	if err := json.Unmarshal(data, &rows); err != nil || len(rows) == 0 {
		_, werr := fmt.Fprintln(w, string(data))
		return werr
	}

	// Collect headers from first row
	headers := make([]string, 0)
	for k := range rows[0] {
		headers = append(headers, k)
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(tw, strings.Join(headers, "\t"))
	for _, row := range rows {
		vals := make([]string, len(headers))
		for i, h := range headers {
			vals[i] = fmt.Sprintf("%v", row[h])
		}
		fmt.Fprintln(tw, strings.Join(vals, "\t"))
	}
	return tw.Flush()
}
