// Package fiberadapter adds Fiber support for the aws-severless-go-api library.
// Uses the core package behind the scenes and exposes the New method to
// get a new instance and Proxy method to send request to the Fiber app.
package fiberadapter

import (
	"context"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"

	"github.com/aws/aws-lambda-go/events"
	"github.com/awslabs/aws-lambda-go-api-proxy/core"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/utils"
	"github.com/valyala/fasthttp"
)

// GinLambdaALB makes it easy to send ALB proxy events to a Fiber
// app. The library transforms the proxy event into an HTTP request and then
// creates a proxy response object from the http.ResponseWriter
type FiberLambdaALB struct {
	core.RequestAccessorALB
	app *fiber.App
}

// New creates a new instance of the FiberLambdaALB object.
// Receives an initialized *fiber.App object - normally created with fiber.New().
// It returns the initialized instance of the FiberLambdaALB object.
func NewALB(app *fiber.App) *FiberLambdaALB {
	return &FiberLambdaALB{
		app: app,
	}
}

// Proxy receives an ALB proxy event, transforms it into an http.Request
// object, and sends it to the fiber.App for routing.
// It returns a proxy response object generated from the http.ResponseWriter
func (f *FiberLambdaALB) Proxy(req events.ALBTargetGroupRequest) (events.ALBTargetGroupResponse, error) {
	httpReq, err := f.ProxyEventToHTTPRequest(req)

	return f.proxyInternal(httpReq, err)
}

// ProxyWithContext receives an ALB proxy event, transforms it into an http.Request
// object, and sends it to the fiber.App for routing.
// It returns a proxy response object generated from the http.ResponseWriter
func (f *FiberLambdaALB) ProxyWithContext(ctx context.Context, req events.ALBTargetGroupRequest) (events.ALBTargetGroupResponse, error) {
	httpReq, err := f.EventToRequestWithContext(ctx, req)

	return f.proxyInternal(httpReq, err)
}

func (f *FiberLambdaALB) proxyInternal(req *http.Request, err error) (events.ALBTargetGroupResponse, error) {
	respWriter := core.NewProxyResponseWriterALB()
	f.adaptor(respWriter, req)

	proxyResponse, err := respWriter.GetProxyResponse()

	return proxyResponse, nil
}

func (f *FiberLambdaALB) adaptor(w http.ResponseWriter, r *http.Request) {
	// New fasthttp request
	req := fasthttp.AcquireRequest()
	defer fasthttp.ReleaseRequest(req)

	// Convert net/http -> fasthttp request
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, utils.StatusMessage(fiber.StatusInternalServerError), fiber.StatusInternalServerError)
		return
	}
	req.Header.SetContentLength(len(body))
	_, _ = req.BodyWriter().Write(body)

	req.Header.SetMethod(r.Method)
	req.SetRequestURI(r.RequestURI)
	req.SetHost(r.Host)
	for key, val := range r.Header {
		for _, v := range val {
			switch key {
			case fiber.HeaderHost,
				fiber.HeaderContentType,
				fiber.HeaderUserAgent,
				fiber.HeaderContentLength,
				fiber.HeaderConnection:
				req.Header.Set(key, v)
			default:
				req.Header.Add(key, v)
			}
		}
	}

	// We need to make sure the net.ResolveTCPAddr call works as it expects a port
	addrWithPort := r.RemoteAddr
	if !strings.Contains(r.RemoteAddr, ":") {
		addrWithPort = r.RemoteAddr + ":80" // assuming a default port
	}

	remoteAddr, err := net.ResolveTCPAddr("tcp", addrWithPort)
	if err != nil {
		fmt.Printf("could not resolve TCP address for addr %s\n", r.RemoteAddr)
		log.Println(err)
		http.Error(w, utils.StatusMessage(fiber.StatusInternalServerError), fiber.StatusInternalServerError)
		return
	}

	// New fasthttp Ctx
	var fctx fasthttp.RequestCtx
	fctx.Init(req, remoteAddr, nil)

	// Pass RequestCtx to Fiber router
	f.app.Handler()(&fctx)

	// Set response headers
	for k, v := range fctx.Response.Header.All() {
		w.Header().Add(utils.UnsafeString(k), utils.UnsafeString(v))
	}

	// Set response statuscode
	w.WriteHeader(fctx.Response.StatusCode())

	// Set response body
	_, _ = w.Write(fctx.Response.Body())
}
