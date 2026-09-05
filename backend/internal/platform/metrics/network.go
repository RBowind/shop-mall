package metrics

import (
	"net"
	"net/http"
	"net/netip"
	"strings"

	"shop-mall/backend/internal/middleware"

	"github.com/gin-gonic/gin"
)

// PrivateNetworkOnly is Gin middleware that serves the wrapped handler only to
// peers whose TCP connection originates from a private, loopback, link-local
// or unspecified address. It is applied to the /metrics route as
// defense-in-depth behind nginx: the edge proxy already refuses public access
// (allow private ranges, deny everything else), and this second check protects
// the exposition even if the application listener is ever reachable directly
// from a public address.
func PrivateNetworkOnly() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !PrivateSource(c.Request) {
			c.AbortWithStatusJSON(http.StatusForbidden, gin.H{
				"code":     4030,
				"data":     nil,
				"message":  "metrics access denied",
				"trace_id": middleware.TraceIDFromGin(c),
			})
			return
		}
		c.Next()
	}
}

// PrivateSource reports whether the peer address of req is private. It reads
// the TCP peer address from RemoteAddr instead of trusting X-Forwarded-For,
// which a remote client can forge. nginx connects from a Docker bridge address
// (172.16.0.0/12) and Prometheus scrapes from the internal backend network, so
// both stay within the private range.
func PrivateSource(req *http.Request) bool {
	if req == nil {
		return false
	}
	host := req.RemoteAddr
	if parsed, _, err := net.SplitHostPort(host); err == nil {
		host = parsed
	}
	host = strings.Trim(host, "[]")
	addr, err := netip.ParseAddr(host)
	if err != nil {
		return false
	}
	return addr.IsPrivate() || addr.IsLoopback() || addr.IsLinkLocalUnicast() || addr.IsUnspecified()
}
