package fiberadapter

import (
	"fmt"
	"github.com/aws/aws-lambda-go/events"
	"github.com/awslabs/aws-lambda-go-api-proxy/core"
	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/fiber/v2/utils"
	"github.com/valyala/fasthttp"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
)

type FiberLambdaALB struct {
	core.RequestAccessorALB
	app *fiber.App
}

func (f *FiberLambdaALB) Proxy(req events.ALBTargetGroupRequest) (events.ALBTargetGroupResponse, interface{}) {
	httpReq, err := f.ProxyEventToHTTPRequest(req)
	return f.proxyInternal(httpReq, err)
}

func NewALB(app *fiber.App) *FiberLambdaALB {
	return &FiberLambdaALB{
		app: app,
	}
}

func (f *FiberLambdaALB) proxyInternal(req *http.Request, err error) (events.ALBTargetGroupResponse, error) {
	respWriter := core.NewProxyResponseWriterALB()
	f.adaptor(respWriter, req)

	proxyResponse, err := respWriter.GetProxyResponse()
	fmt.Printf("proxyResponse: %+v\n", proxyResponse)
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
