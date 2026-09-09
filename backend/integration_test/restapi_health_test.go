//go:build integration

package integration_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/getkin/kin-openapi/openapi3filter"
	"github.com/stretchr/testify/require"

	"github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/api"
	restv1 "github.com/TakuyaYagam1/task-per-minute/internal/adapter/inbound/http/v1"
)

func TestRESTHandlers_HealthDegradedShape(t *testing.T) {
	server := restv1.New(restv1.Dependencies{
		Health: restv1.HealthChecks{
			DB: restv1.HealthCheckerFunc(func(context.Context) error {
				return errors.New("db down")
			}),
			Redis: restv1.HealthCheckerFunc(func(context.Context) error {
				return nil
			}),
			SeaweedFS: restv1.HealthCheckerFunc(func(context.Context) error {
				return nil
			}),
		},
	})
	handler := restv1.NewHandler(server, restv1.HandlerOptions{})
	validator := newOpenAPIResponseValidator(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	require.Equal(t, http.StatusServiceUnavailable, resp.Code)
	route, pathParams, err := validator.FindRoute(req)
	require.NoError(t, err)
	input := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{
			Request:    req,
			PathParams: pathParams,
			Route:      route,
		},
		Status: resp.Code,
		Header: resp.Result().Header,
	}
	input.SetBodyBytes(resp.Body.Bytes())
	require.NoError(t, openapi3filter.ValidateResponse(context.Background(), input))
}

func TestRESTHandlers_HealthSchemaVersionZeroIsDegraded(t *testing.T) {
	server := restv1.New(restv1.Dependencies{
		Health: restv1.HealthChecks{
			DB: restv1.HealthCheckerFunc(func(context.Context) error {
				return nil
			}),
			Redis: restv1.HealthCheckerFunc(func(context.Context) error {
				return nil
			}),
			SeaweedFS: restv1.HealthCheckerFunc(func(context.Context) error {
				return nil
			}),
			SchemaVersion: restv1.SchemaVersionReaderFunc(func(context.Context) (int64, error) {
				return 0, nil
			}),
		},
	})
	handler := restv1.NewHandler(server, restv1.HandlerOptions{})
	validator := newOpenAPIResponseValidator(t)

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	resp := httptest.NewRecorder()
	handler.ServeHTTP(resp, req)

	require.Equal(t, http.StatusServiceUnavailable, resp.Code)
	got := decodeJSON[api.HealthResponse](t, resp)
	require.Equal(t, api.HealthResponseStatusDegraded, got.Status)
	require.Equal(t, int64(0), got.SchemaVersion)

	route, pathParams, err := validator.FindRoute(req)
	require.NoError(t, err)
	input := &openapi3filter.ResponseValidationInput{
		RequestValidationInput: &openapi3filter.RequestValidationInput{
			Request:    req,
			PathParams: pathParams,
			Route:      route,
		},
		Status: resp.Code,
		Header: resp.Result().Header,
	}
	input.SetBodyBytes(resp.Body.Bytes())
	require.NoError(t, openapi3filter.ValidateResponse(context.Background(), input))
}
