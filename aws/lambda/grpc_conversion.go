package lambda

import (
	"errors"
	"fmt"
	"net/http"

	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func convertResultError(res *Response, err any) error {

	if err, ok := err.(error); ok {

		if err, ok := status.FromError(err); ok {

			res.Body = err.Message()
			res.IsBase64Encoded = false

			switch err.Code() {

			case codes.InvalidArgument:
				res.StatusCode = http.StatusBadRequest

			case codes.NotFound:
				res.StatusCode = http.StatusNotFound

			case codes.AlreadyExists:
				res.StatusCode = http.StatusConflict

			case codes.PermissionDenied:
				res.StatusCode = http.StatusForbidden

			case codes.Unauthenticated:
				res.StatusCode = http.StatusUnauthorized

			case codes.ResourceExhausted:
				res.StatusCode = http.StatusTooManyRequests

			case codes.FailedPrecondition:
				res.StatusCode = http.StatusPreconditionFailed

			case codes.Aborted:
				res.StatusCode = http.StatusConflict

			case codes.OutOfRange:
				res.StatusCode = http.StatusBadRequest

			case codes.Unimplemented:
				res.StatusCode = http.StatusNotImplemented

			case codes.Internal:
				res.StatusCode = http.StatusInternalServerError

			case codes.Unavailable:
				res.StatusCode = http.StatusServiceUnavailable

			case codes.DataLoss:
				res.StatusCode = http.StatusInternalServerError

			}

		}

		return err
	}

	res.StatusCode = http.StatusInternalServerError
	res.Body = fmt.Sprintf("Invalid error type: %T", err)

	return errors.New(res.Body)

}
