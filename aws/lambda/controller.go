package lambda

import (
	"context"
	"encoding/base64"
	"fmt"
	"net/http"
	"reflect"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/protomesh/go-app"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/metadata"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/proto"
)

type Request struct {
	*events.APIGatewayProxyRequest
	HandlerKey string
}

func (r *Request) UnmarshalProtobuf(m proto.Message) error {

	if r.IsBase64Encoded {
		msg, err := base64.RawStdEncoding.DecodeString(r.Body)
		if err != nil {
			return err
		}
		return proto.Unmarshal(msg, m)
	}

	return proto.Unmarshal([]byte(r.Body), m)
}

type Response struct {
	*events.APIGatewayProxyResponse
}

func (r *Response) MarshalProtobuf(m proto.Message) error {
	body, err := proto.Marshal(m)
	if err != nil {
		return err
	}

	if len(body) > 0 {
		r.Body = base64.RawStdEncoding.EncodeToString(body)
		r.IsBase64Encoded = true
		return nil
	}

	r.Body = ""
	r.IsBase64Encoded = false

	return nil
}

type Matcher[K comparable] func(context.Context, *events.APIGatewayProxyRequest) (K, error)

type Handler func(context.Context, *Request, *Response) error

type ControllerDependency interface {
}

type Controller[D ControllerDependency] struct {
	*app.Injector[D]

	Matcher Matcher[string]

	handlers map[string]Handler
}

func NewController[D ControllerDependency]() *Controller[D] {
	return &Controller[D]{
		handlers: make(map[string]Handler),
	}
}

func (c *Controller[D]) RegisterHandler(key string, handler Handler) {
	c.handlers[key] = handler
}

func (c *Controller[D]) RegisterGRPCService(desc grpc.ServiceDesc, svc interface{}) {

	reflectSvc := reflect.ValueOf(svc)

	for _, method := range desc.Methods {

		key := strings.Join([]string{"/", desc.ServiceName, "/", method.MethodName}, "")

		methodCaller := reflectSvc.MethodByName(method.MethodName)
		methodType := methodCaller.Type()

		methodInput := reflect.New(methodType.In(1).Elem()).Interface().(proto.Message)

		c.RegisterHandler(key, func(ctx context.Context, req *Request, res *Response) error {

			inMeta := metadata.Join(metadata.New(req.Headers), req.MultiValueHeaders)

			callCtx := metadata.NewOutgoingContext(metadata.NewIncomingContext(ctx, inMeta), metadata.New(map[string]string{}))
			callInput := proto.Clone(methodInput)

			if err := req.UnmarshalProtobuf(callInput); err != nil {
				res.StatusCode = http.StatusBadRequest
				res.Body = fmt.Sprintf("Failed to unmarshal request: %v", err)
				return err
			}

			result := methodCaller.Call([]reflect.Value{
				reflect.ValueOf(callCtx),
				reflect.ValueOf(callInput),
			})

			if outMeta, ok := metadata.FromOutgoingContext(ctx); ok {
				res.MultiValueHeaders = outMeta
			}

			if len(result) != 2 {
				res.StatusCode = http.StatusInternalServerError
				res.Body = fmt.Sprintf("Invalid method output: %+v", result)
				return nil
			}

			err := result[1].Interface()
			if err != nil {
				return convertResultError(res, err)
			}

			out := result[0].Interface()

			if out == nil {
				res.Body = ""
				return nil
			} else if err := res.MarshalProtobuf(out.(proto.Message)); err != nil {
				res.StatusCode = http.StatusInternalServerError
				res.Body = fmt.Sprintf("Failed to marshal response: %v", err)
				return err
			}

			return nil

		})
	}

	for _, stream := range desc.Streams {

		key := strings.Join([]string{"/", desc.ServiceName, "/", stream.StreamName}, "")

		if stream.ServerStreams && !stream.ClientStreams {

			c.RegisterHandler(key, func(ctx context.Context, req *Request, res *Response) error {

				inMeta := metadata.Join(metadata.New(req.Headers), req.MultiValueHeaders)

				callCtx := metadata.NewOutgoingContext(metadata.NewIncomingContext(ctx, inMeta), metadata.New(map[string]string{}))

				serverStream := newGrpcServerStream(callCtx, req, res)

				err := stream.Handler(svc, serverStream)

				res.MultiValueHeaders, _ = metadata.FromOutgoingContext(serverStream.ctx)

				if err != nil {
					return convertResultError(res, err)
				}

				res.APIGatewayProxyResponse.StatusCode = http.StatusProcessing

				return nil

			})

		}

	}

}

func (c *Controller[D]) HandleLambda(ctx context.Context, proxyReq *events.APIGatewayProxyRequest) (*events.APIGatewayProxyResponse, error) {

	log := c.Log()

	res := &Response{
		APIGatewayProxyResponse: &events.APIGatewayProxyResponse{
			StatusCode: http.StatusNotFound,
		},
	}

	key, err := c.Matcher(ctx, proxyReq)
	if err != nil {

		if st, ok := status.FromError(err); ok && st.Code() == codes.NotFound {
			log.Error("Matcher returned not found", "error", err)
			return res.APIGatewayProxyResponse, nil
		}

		log.Error("Failed to match request", "error", err)
		res.StatusCode = http.StatusInternalServerError
		return res.APIGatewayProxyResponse, nil

	}

	handler, ok := c.handlers[key]
	if !ok {
		log.Error("No handler registered for key", "key", key, "handlers", fmt.Sprintf("%+v", c.handlers))
		return res.APIGatewayProxyResponse, nil
	}

	req := &Request{
		APIGatewayProxyRequest: proxyReq,
		HandlerKey:             key,
	}

	res.APIGatewayProxyResponse.StatusCode = http.StatusOK

	if err := handler(ctx, req, res); err != nil {
		log.Error("Failed to handle request", "error", err)
		if res.StatusCode < 400 {
			res.StatusCode = http.StatusInternalServerError
		}
	}

	return res.APIGatewayProxyResponse, nil
}

func MakeUrlPathMatcher(basePath string) Matcher[string] {

	basePath = strings.TrimRight(basePath, "/")

	return func(ctx context.Context, req *events.APIGatewayProxyRequest) (string, error) {

		urlPath := strings.TrimRight(req.Path, "/")

		if len(basePath) == 0 {
			return urlPath, nil
		}

		if !strings.HasPrefix(urlPath, basePath) {
			return "", grpc.Errorf(codes.NotFound, "Not found (couldn't match prefix %s for url path %s)", basePath, urlPath)
		}

		return strings.TrimPrefix(urlPath, basePath), nil

	}
}
