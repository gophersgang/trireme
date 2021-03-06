package rpcwrapper

import (
	"bytes"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"encoding/gob"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"time"

	"net/rpc"

	"github.com/aporeto-inc/trireme/cache"
)

//RPCHdl is a per client handle
type RPCHdl struct {
	Client  *rpc.Client
	Channel string
	Secret  string
}

//RPCWrapper  is a struct which holds stats for all rpc sesions
type RPCWrapper struct {
	rpcClientMap *cache.Cache
	contextList  []string
}

//NewRPCWrapper creates a new rpcwrapper
func NewRPCWrapper() *RPCWrapper {
	rpcwrapper := &RPCWrapper{
		rpcClientMap: cache.NewCache(),
		contextList:  []string{},
	}

	rpcwrapper.rpcClientMap = cache.NewCache()
	return rpcwrapper
}

const (
	maxRetries     = 1000
	envRetryString = "REMOTE_RPCRETRIES"
)

//NewRPCClient exported
//Will worry about locking later ... there is a small case where two callers
//call NewRPCClient from a different thread
func (r *RPCWrapper) NewRPCClient(contextID string, channel string, sharedsecret string) error {

	//establish new connection to context/container
	RegisterTypes()
	var max int
	retries := os.Getenv(envRetryString)
	if len(retries) > 0 {
		max, _ = strconv.Atoi(retries)

	} else {
		max = maxRetries
	}
	numRetries := 0
	client, err := rpc.DialHTTP("unix", channel)

	for err != nil {
		time.Sleep(5 * time.Millisecond)

		numRetries = numRetries + 1
		if numRetries < max {
			client, err = rpc.DialHTTP("unix", channel)
		} else {
			return err
		}
	}

	r.contextList = append(r.contextList, contextID)
	return r.rpcClientMap.Add(contextID, &RPCHdl{Client: client, Channel: channel, Secret: sharedsecret})

}

//GetRPCClient gets a handle to the rpc client for the contextID( enforcer in the container)
func (r *RPCWrapper) GetRPCClient(contextID string) (*RPCHdl, error) {

	val, err := r.rpcClientMap.Get(contextID)
	if err == nil {
		return val.(*RPCHdl), err
	}
	return nil, err
}

//RemoteCall is a wrapper around rpc.Call and also ensure message integrity by adding a hmac
func (r *RPCWrapper) RemoteCall(contextID string, methodName string, req *Request, resp *Response) error {

	var rpcBuf bytes.Buffer
	binary.Write(&rpcBuf, binary.BigEndian, req.Payload)
	rpcClient, err := r.GetRPCClient(contextID)
	if err != nil {
		return err
	}

	digest := hmac.New(sha256.New, []byte(rpcClient.Secret))
	digest.Write(rpcBuf.Bytes())
	req.HashAuth = digest.Sum(nil)

	return rpcClient.Client.Call(methodName, req, resp)

}

//CheckValidity checks if the received message is valid
func (r *RPCWrapper) CheckValidity(req *Request, secret string) bool {

	var rpcBuf bytes.Buffer
	binary.Write(&rpcBuf, binary.BigEndian, req.Payload)
	digest := hmac.New(sha256.New, []byte(secret))
	digest.Write(rpcBuf.Bytes())
	return hmac.Equal(req.HashAuth, digest.Sum(nil))
}

//NewRPCServer returns an interface RPCServer
func NewRPCServer() RPCServer {

	return &RPCWrapper{}
}

//StartServer Starts a server and waits for new connections this function never returns
func (r *RPCWrapper) StartServer(protocol string, path string, handler interface{}) error {

	RegisterTypes()

	rpc.Register(handler)
	rpc.HandleHTTP()
	os.Remove(path)
	if len(path) == 0 {
		log.Fatal("Sock param not passed in environment")
	}
	listen, err := net.Listen(protocol, path)

	if err != nil {

		return err
	}
	go http.Serve(listen, nil)
	defer func() {
		listen.Close()
		os.Remove(path)
	}()
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt)
	<-c

	_, err = os.Stat(path)
	if !os.IsNotExist(err) {
		os.Remove(path)
	}
	return nil
}

// DestroyRPCClient calls close on the rpc and cleans up the connection
func (r *RPCWrapper) DestroyRPCClient(contextID string) {

	rpcHdl, _ := r.rpcClientMap.Get(contextID)
	rpcHdl.(*RPCHdl).Client.Close()
	os.Remove(rpcHdl.(*RPCHdl).Channel)
	r.rpcClientMap.Remove(contextID)
}

// ProcessMessage checks if the given request is valid
func (r *RPCWrapper) ProcessMessage(req *Request, secret string) bool {

	return r.CheckValidity(req, secret)
}

// ContextList returns the list of active context managed by the rpcwrapper
func (r *RPCWrapper) ContextList() []string {
	return r.contextList
}

// RegisterTypes  registers types that are exchanged between the controller and remoteenforcer
func RegisterTypes() {

	gob.RegisterName("github.com/aporeto-inc/enforcer/utils/rpcwrapper.Init_Request_Payload", *(&InitRequestPayload{}))
	gob.RegisterName("github.com/aporeto-inc/enforcer/utils/rpcwrapper.Init_Response_Payload", *(&InitResponsePayload{}))
	gob.RegisterName("github.com/aporeto-inc/enforcer/utils/rpcwrapper.Init_Supervisor_Payload", *(&InitSupervisorPayload{}))

	gob.RegisterName("github.com/aporeto-inc/enforcer/utils/rpcwrapper.Enforce_Payload", *(&EnforcePayload{}))
	gob.RegisterName("github.com/aporeto-inc/enforcer/utils/rpcwrapper.UnEnforce_Payload", *(&UnEnforcePayload{}))

	gob.RegisterName("github.com/aporeto-inc/enforcer/utils/rpcwrapper.Supervise_Request_Payload", *(&SuperviseRequestPayload{}))
	gob.RegisterName("github.com/aporeto-inc/enforcer/utils/rpcwrapper.UnSupervise_Payload", *(&UnSupervisePayload{}))
	gob.RegisterName("github.com/aporeto-inc/enforcer/utils/rpcwrapper.Stats_Payload", *(&StatsPayload{}))
	gob.RegisterName("github.com/aporeto-inc/enforcer/utils/rpcwrapper.ExcludeIPRequestPayload", *(&ExcludeIPRequestPayload{}))
}
