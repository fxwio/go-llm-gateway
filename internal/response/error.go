package response

import (
	"encoding/json"
	"net/http"
)

type OpenAIErrorEnvelope struct {
	Error OpenAIError `json:"error"`
}

type OpenAIError struct {
	Message string  `json:"message"`
	Type    string  `json:"type"`
	Param   *string `json:"param"`
	Code    *string `json:"code"`
}

func Ptr(s string) *string {
	if s == "" {
		return nil
	}
	return &s
}

func WriteOpenAIError(
	w http.ResponseWriter,
	status int,
	message string,
	errType string,
	param *string,
	code *string,
) {
	if errType == "" {
		errType = defaultErrorType(status)
	}

	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(status)

	_ = json.NewEncoder(w).Encode(OpenAIErrorEnvelope{
		Error: OpenAIError{
			Message: message,
			Type:    errType,
			Param:   param,
			Code:    code,
		},
	})
}

func defaultErrorType(status int) string {
	switch status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return "authentication_error"
	case http.StatusTooManyRequests:
		return "rate_limit_error"
	case http.StatusBadRequest, http.StatusNotFound, http.StatusRequestEntityTooLarge:
		return "invalid_request_error"
	default:
		return "server_error"
	}
}
