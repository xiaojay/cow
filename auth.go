package main

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/cyfdecyf/bufio"
	"net"
	"os"
	"strconv"
	"strings"
	"text/template"
	"time"
	"github.com/vmihailenco/redis"
    "encoding/base64"
)

const (
    authRealm2       = "Jayproxy"
    authRawBodyTmpl2 = `<!DOCTYPE html>
<html>
    <head> <title>JayProxy</title> </head>
    <body>
        <h1>407 Proxy authentication required</h1>
        <hr />
        Generated by <i>jayproxy</i>
    </body>
</html>
`
)

var rclient *redis.Client
func initRedis(){
	rclient = redis.NewTCPClient("localhost:6379", "", -1)
}

func b64decode(data string) (r string, err error ){
    d, err := base64.StdEncoding.DecodeString(data)
    if err != nil{
        return "", err
    }
    r = fmt.Sprintf("%q", d)
    return r, nil
}

func parseUserPassword(data string) (name string, password string, err error){
    d, err := b64decode(data)
    if err != nil{
        return "", "", errors.New("Base64 decoded Wrong:" + data + err.Error())
    }
    s := strings.Trim(d, "\"")
    r := strings.Split(s, ":")
    if len(r) != 2{
        return "", "", errors.New("Parse user and password wrong")
    }
    return r[0], r[1], nil
}

type netAddr struct {
	ip   net.IP
	mask net.IPMask
}

type authUser struct {
	// user name is the key to auth.user, no need to store here
	passwd string
	ha1    string // used in request digest, initialized ondemand
	port   uint16 // 0 means any port
	ip string

}

var auth struct {
	required bool

	user map[string]*authUser

	allowedClient []netAddr

	authed *TimeoutSet // cache authenticated users based on ip

	template *template.Template
}

func (au *authUser) initHA1(user string) {
	if au.ha1 == "" {
		au.ha1 = md5sum(user + ":" + authRealm + ":" + au.passwd)
	}
}

func getAuth(user string) authUser {
	info.Println("user", user)
    r := rclient.HGet("password", user)
	password := r.Val()
    info.Println("password", password)
	if password == "" {
		return authUser{}
	}

	r = rclient.HGet("ip", user)
	ip := r.Val()

	au := authUser{password, "", 0, ip}
	return au
}

func initAuth() {
	auth.required = true
	initRedis()

	auth.user = make(map[string]*authUser)
	auth.authed = NewTimeoutSet(time.Duration(config.AuthTimeout) * time.Hour)
    
	rawTemplate := "HTTP/1.1 407 Proxy Authentication Required\r\n" +
		"Proxy-Authenticate: Basic realm=\"" + authRealm2 + "\"\r\n" +
		"Content-Type: text/html\r\n" +
		"Cache-Control: no-cache\r\n" +
		"Content-Length: " + fmt.Sprintf("%d", len(authRawBodyTmpl2)) + "\r\n\r\n" + authRawBodyTmpl2
	var err error
	if auth.template, err = template.New("auth").Parse(rawTemplate); err != nil {
		Fatal("internal error generating auth template:", err)
	}
}

// Return err = nil if authentication succeed. nonce would be not empty if
// authentication is needed, and should be passed back on subsequent call.
func Authenticate(conn *clientConn, r *Request) (err error) {
	clientIP, _ := splitHostPort(conn.RemoteAddr().String())
    //clientIP :=  r.XForwardFor
    //info.Printf("\033[0;31;48mthis request ip is\033[0m: %s", clientIP)
    //info.Printf("\033[0;31;48mCurrent authed ips\033[0m: %s", auth.authed.get_keys_display())
    //if auth.authed.has(clientIP) {
		//info.Printf("%s has already authed\n", clientIP)
		//return
	//}
	if authIP2(clientIP) { // IP is allowed
        return
	}
	err = authUserPasswd(conn, r)
	return
}

// authIP checks whether the client ip address matches one in allowedClient.
// It uses a sequential search.
func authIP(clientIP string) bool {
	ip := net.ParseIP(clientIP)
	if ip == nil {
		panic("authIP should always get IP address")
	}

	for _, na := range auth.allowedClient {
		if ip.Mask(na.mask).Equal(na.ip) {
			debug.Printf("client ip %s allowed\n", clientIP)
			return true
		}
	}
	return false
}

func authIP2(clientIP string) bool {
	ip := net.ParseIP(clientIP)
	if ip == nil {
		panic("authIP should always get IP address")
	}
    r := rclient.SIsMember("authed_ips", ip)
    return r.Val()
}

func genNonce() string {
	buf := new(bytes.Buffer)
	fmt.Fprintf(buf, "%x", time.Now().Unix())
	return buf.String()
}

func calcRequestDigest(kv map[string]string, ha1, method string) string {
	// Refer to rfc2617 section 3.2.2.1 Request-Digest
	buf := bytes.NewBufferString(ha1)
	buf.WriteByte(':')
	buf.WriteString(kv["nonce"])
	buf.WriteByte(':')
	buf.WriteString(kv["nc"])
	buf.WriteByte(':')
	buf.WriteString(kv["cnonce"])
	buf.WriteByte(':')
	buf.WriteString("auth") // qop value
	buf.WriteByte(':')
	buf.WriteString(md5sum(method + ":" + kv["uri"]))

	return md5sum(buf.String())
}

func checkProxyAuthorization(conn *clientConn, r *Request) error {
	info.Println("authorization:", r.ProxyAuthorization)
	arr := strings.SplitN(r.ProxyAuthorization, " ", 2)
	if len(arr) != 2 {
		errl.Println("auth: malformed ProxyAuthorization header:", r.ProxyAuthorization)
		return errBadRequest
	}
	if strings.ToLower(strings.TrimSpace(arr[0])) != "basic" {
		errl.Println("auth: client using unsupported authenticate method:", arr[0])
		return errBadRequest
	}
	//handle error
    name, password, err := parseUserPassword(arr[1])
    if err != nil{
        errl.Println("Parse user and password wrong using base64:", arr[1])
        return errBadRequest
    }
    info.Println("Get User:", name)
    info.Println("password", password)

	au := getAuth(name)
	if au.passwd == "" {
		errl.Println("auth: no such user:", name)
		return errAuthRequired
	}

	if au.port != 0 {
		// check port
		_, portStr := splitHostPort(conn.LocalAddr().String())
		port, _ := strconv.Atoi(portStr)
		if uint16(port) != au.port {
			errl.Println("auth: user", name, "port not match")
			return errAuthRequired
		}
	}

	if password == au.passwd {
		clientIP, _ := splitHostPort(conn.RemoteAddr().String())
	    //clientIP := r.XForwardFor
        auth.authed.add(clientIP)
        rclient.SAdd("authed_ips", clientIP)
		info.Printf("'\033[0;31;48mAdd new ip %s for %s\033[0m", clientIP, name)
        if au.ip != clientIP{
			auth.authed.del(au.ip)
		    info.Printf("'\033[0;31;48mDel %s's ip %s\033[0m", name, au.ip)
			rclient.HSet("ip", name, clientIP)
		}
		return nil
	}
	errl.Println("auth: password wrong")
	return errAuthRequired
}

func authUserPasswd(conn *clientConn, r *Request) (err error) {
	if r.ProxyAuthorization != "" {
		// client has sent authorization header
		err = checkProxyAuthorization(conn, r)
		if err == nil {
			return
		} else if err != errAuthRequired {
			sendErrorPage(conn, statusBadReq, "Bad authorization request", err.Error())
			return
		}
		// auth required to through the following
	}

	nonce := genNonce()
	data := struct {
		Nonce string
	}{
		nonce,
	}
	buf := new(bytes.Buffer)
	if err := auth.template.Execute(buf, data); err != nil {
		errl.Println("Error generating auth response:", err)
		return errInternal
	}
	if info {
		info.Printf("authorization response:\n%s", buf.String())
	}
	if _, err := conn.Write(buf.Bytes()); err != nil {
		errl.Println("Sending auth response error:", err)
		return errShouldClose
	}
	return errAuthRequired
}
