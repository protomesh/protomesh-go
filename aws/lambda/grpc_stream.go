package lambda

import (
	"context"
	"fmt"
	"net/http"

	"google.golang.org/grpc/metadata"
	"google.golang.org/protobuf/proto"
)

type grpcServerStream struct {
	ctx context.Context
	req *Request
	res *Response
}

func newGrpcServerStream(ctx context.Context, req *Request, res *Response) *grpcServerStream {
	return &grpcServerStream{
		ctx: ctx,
		req: req,
		res: res,
	}
}

func (g *grpcServerStream) Context() context.Context {
	return g.ctx
}

func (g *grpcServerStream) SendMsg(m interface{}) error {

	if m == nil {
		g.res.Body = ""
		return nil
	} else if err := g.res.MarshalProtobuf(m.(proto.Message)); err != nil {
		g.res.StatusCode = http.StatusInternalServerError
		g.res.Body = fmt.Sprintf("Failed to marshal response: %v", err)
		return err
	}

	return nil
}

func (g *grpcServerStream) RecvMsg(m interface{}) error {

	err := g.req.UnmarshalProtobuf(m.(proto.Message))

	if err != nil {
		g.res.StatusCode = http.StatusBadRequest
		g.res.Body = fmt.Sprintf("Failed to unmarshal request: %v", err)
	}

	return err

}

func (g *grpcServerStream) SendHeader(m metadata.MD) error {
	g.SetTrailer(m)
	return nil
}

func (g *grpcServerStream) SetHeader(m metadata.MD) error {
	g.SetTrailer(m)
	return nil
}

func (g *grpcServerStream) SetTrailer(m metadata.MD) {
	outMeta, _ := metadata.FromOutgoingContext(g.ctx)
	g.ctx = metadata.NewOutgoingContext(g.ctx, metadata.Join(outMeta, m))
}
