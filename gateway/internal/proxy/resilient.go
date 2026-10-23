package proxy

import (
	"bytes"
	"io"
	"net/http"
	"net/url"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	resilience "github.com/kyurem-project/kyurem/gateway/internal/failure"
)

type attemptTarget struct {
	key string
	url *url.URL
}

type attemptPlan struct {
	agent     string
	targets   []attemptTarget
	retryable bool
}

type failureTransport struct {
	base    http.RoundTripper
	manager *resilience.Manager
}

func (t *failureTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	if err := req.Context().Err(); err != nil {
		return nil, err
	}
	plan, ok := req.Context().Value(ctxFailurePlan).(attemptPlan)
	if !ok || len(plan.targets) == 0 || t.manager == nil {
		return t.base.RoundTrip(req)
	}

	maxAttempts := t.manager.MaxAttempts()
	attempts, checked, next := 0, 0, 0
	maxChecks := maxAttempts * len(plan.targets)
	var lastResponse *http.Response
	var lastError error
	previousTarget := -1

	for attempts < maxAttempts && checked < maxChecks {
		targetIndex := next % len(plan.targets)
		next++
		checked++
		target := plan.targets[targetIndex]
		retrying := attempts > 0
		if retrying && !plan.retryable {
			break
		}
		if retrying && hasRequestBody(req) && req.GetBody == nil {
			break
		}
		if !t.manager.Begin(target.key, plan.agent, retrying) {
			continue
		}

		attemptReq := req.Clone(req.Context())
		attemptReq.URL = target.url
		attemptReq.Host = target.url.Host
		if retrying && hasRequestBody(req) {
			body, err := req.GetBody()
			if err != nil {
				t.manager.Complete(target.key, false)
				lastError = err
				break
			}
			attemptReq.Body = body
		}
		if lastResponse != nil {
			_ = lastResponse.Body.Close()
			lastResponse = nil
		}
		if retrying {
			span := trace.SpanFromContext(req.Context())
			attrs := []attribute.KeyValue{
				attribute.Int("kyurem.attempt", attempts+1),
				attribute.Bool("kyurem.failover", previousTarget >= 0 && previousTarget != targetIndex),
			}
			span.AddEvent("kyurem.retry", trace.WithAttributes(attrs...))
			if previousTarget >= 0 && previousTarget != targetIndex {
				span.AddEvent("kyurem.failover", trace.WithAttributes(attribute.Int("kyurem.attempt", attempts+1)))
			}
		}
		previousTarget = targetIndex
		attempts++

		response, err := t.base.RoundTrip(attemptReq)
		lastError = err
		if err == nil {
			t.manager.Complete(target.key, response.StatusCode < http.StatusInternalServerError)
			if !retryableStatus(response.StatusCode) || !plan.retryable || attempts >= maxAttempts {
				return response, nil
			}
			lastResponse = response
			continue
		}
		t.manager.Complete(target.key, false)
		if !plan.retryable || attempts >= maxAttempts {
			return nil, err
		}
	}

	if lastResponse != nil {
		return lastResponse, nil
	}
	if attempts == 0 {
		return unavailableResponse(req), nil
	}
	return nil, lastError
}

func retryableStatus(status int) bool {
	return status == http.StatusBadGateway || status == http.StatusServiceUnavailable || status == http.StatusGatewayTimeout
}

func unavailableResponse(req *http.Request) *http.Response {
	const body = `{"error":"upstreams_unavailable","message":"all configured upstreams are unavailable"}`
	return &http.Response{
		Status:        "503 Service Unavailable",
		StatusCode:    http.StatusServiceUnavailable,
		Header:        http.Header{"Content-Type": []string{"application/json"}},
		Body:          io.NopCloser(strings.NewReader(body)),
		ContentLength: int64(len(body)),
		Request:       req,
	}
}

func prepareRetry(req *http.Request, maxBody int64) bool {
	if isSafeMethod(req.Method) && requestBodyEmpty(req) {
		return true
	}
	if req.Method != http.MethodPost || strings.TrimSpace(req.Header.Get("Idempotency-Key")) == "" {
		return false
	}
	if requestBodyEmpty(req) {
		return true
	}
	if req.GetBody != nil {
		return true
	}
	if maxBody <= 0 || req.ContentLength < 0 || req.ContentLength > maxBody {
		return false
	}

	original := req.Body
	data, err := io.ReadAll(io.LimitReader(original, maxBody+1))
	if err != nil || int64(len(data)) > maxBody {
		req.Body = &replayPrefix{Reader: io.MultiReader(bytes.NewReader(data), original), closer: original}
		return false
	}
	_ = original.Close()
	replay := append([]byte(nil), data...)
	req.Body = io.NopCloser(bytes.NewReader(replay))
	req.GetBody = func() (io.ReadCloser, error) {
		return io.NopCloser(bytes.NewReader(replay)), nil
	}
	return true
}

func isSafeMethod(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return true
	default:
		return false
	}
}

func requestBodyEmpty(req *http.Request) bool {
	return req.Body == nil || req.Body == http.NoBody || req.ContentLength == 0
}

func hasRequestBody(req *http.Request) bool { return !requestBodyEmpty(req) }

type replayPrefix struct {
	io.Reader
	closer io.Closer
}

func (r *replayPrefix) Close() error { return r.closer.Close() }
