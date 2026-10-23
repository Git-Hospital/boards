// Package prom is a minimal client for Prometheus' instant query API: the
// one call oracles need.
package prom

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

// Client queries one Prometheus.
type Client struct {
	Base string // e.g. http://127.0.0.1:9090
	HTTP *http.Client
}

// New returns a client for base.
func New(base string) *Client {
	return &Client{Base: base, HTTP: &http.Client{Timeout: 15 * time.Second}}
}

// Sample is one series' value.
type Sample struct {
	Labels map[string]string
	Value  float64
}

type response struct {
	Status string `json:"status"`
	Error  string `json:"error"`
	Data   struct {
		ResultType string          `json:"resultType"`
		Result     json.RawMessage `json:"result"`
	} `json:"data"`
}

// Query runs an instant query. A vector comes back as its samples, a scalar
// as one unlabelled sample. NaN values are returned as they are: 0/0 is how
// a ratio says "no traffic", and the caller decides what that means.
func (c *Client) Query(ctx context.Context, q string) ([]Sample, error) {
	u := c.Base + "/api/v1/query?" + url.Values{"query": {q}}.Encode()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return nil, err
	}
	var r response
	if err := json.Unmarshal(body, &r); err != nil {
		return nil, fmt.Errorf("prometheus: %s: %w", resp.Status, err)
	}
	if r.Status != "success" {
		return nil, fmt.Errorf("prometheus: %s", r.Error)
	}
	switch r.Data.ResultType {
	case "vector":
		var vec []struct {
			Metric map[string]string `json:"metric"`
			Value  [2]any            `json:"value"`
		}
		if err := json.Unmarshal(r.Data.Result, &vec); err != nil {
			return nil, err
		}
		out := make([]Sample, 0, len(vec))
		for _, s := range vec {
			v, err := value(s.Value)
			if err != nil {
				return nil, err
			}
			out = append(out, Sample{Labels: s.Metric, Value: v})
		}
		return out, nil
	case "scalar":
		var s [2]any
		if err := json.Unmarshal(r.Data.Result, &s); err != nil {
			return nil, err
		}
		v, err := value(s)
		if err != nil {
			return nil, err
		}
		return []Sample{{Value: v}}, nil
	default:
		return nil, fmt.Errorf("prometheus: result type %q is not supported; an oracle needs a vector or a scalar", r.Data.ResultType)
	}
}

func value(pair [2]any) (float64, error) {
	s, ok := pair[1].(string)
	if !ok {
		return 0, fmt.Errorf("prometheus: value %v is not a string", pair[1])
	}
	return strconv.ParseFloat(s, 64)
}
