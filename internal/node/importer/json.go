package importer

import (
	"encoding/json"
	"strings"

	"github.com/bsfdsagfadg/vertex/internal/node/exitpool"
)

func applyCommonImportedProxyFields(proxy map[string]any, obj map[string]any) {
	if sni := strings.TrimSpace(ValueToString(obj["Sni"])); sni != "" {
		proxy["sni"] = sni
		proxy["servername"] = sni
	}
	if fp := strings.TrimSpace(ValueToString(obj["Fingerprint"])); fp != "" {
		proxy["client-fingerprint"] = fp
		proxy["fingerprint"] = fp
	}
	if importedAllowInsecure(obj["AllowInsecure"]) {
		proxy["skip-cert-verify"] = true
	}
	if alpn := splitCSV(ValueToString(obj["Alpn"])); len(alpn) > 0 {
		proxy["alpn"] = alpn
	}
	if cert := strings.TrimSpace(ValueToString(obj["Cert"])); cert != "" {
		proxy["certificate"] = cert
	}
	if privateKey := strings.TrimSpace(ValueToString(obj["PrivateKey"])); privateKey != "" {
		proxy["private-key"] = privateKey
	}
}

func applyTransportExtras(proxy map[string]any, obj map[string]any, transport map[string]any) {
	network := normalizeImportedNetwork(ValueToString(obj["Network"]))
	if network == "" {
		return
	}

	switch network {
	case "ws":
		proxy["network"] = "ws"
		wsOpts := map[string]any{}
		if path := strings.TrimSpace(ValueToString(transport["Path"])); path != "" {
			wsOpts["path"] = path
		}
		if host := strings.TrimSpace(ValueToString(transport["Host"])); host != "" {
			wsOpts["headers"] = map[string]any{"Host": host}
		}
		if ed := strings.TrimSpace(firstNonEmpty(ValueToString(transport["MaxEarlyData"]), ValueToString(transport["max-early-data"]), ValueToString(transport["max_early_data"]), ValueToString(transport["ed"]))); ed != "" {
			wsOpts["max-early-data"] = ed
		}
		if edHeader := strings.TrimSpace(firstNonEmpty(ValueToString(transport["EarlyDataHeaderName"]), ValueToString(transport["early-data-header-name"]), ValueToString(transport["early_data_header_name"]))); edHeader != "" {
			wsOpts["early-data-header-name"] = edHeader
		}
		if len(wsOpts) > 0 {
			proxy["ws-opts"] = wsOpts
		}
	case "grpc":
		proxy["network"] = "grpc"
		grpcOpts := map[string]any{}
		if serviceName := strings.TrimSpace(ValueToString(transport["GrpcServiceName"])); serviceName != "" {
			grpcOpts["grpc-service-name"] = serviceName
		}
		if len(grpcOpts) > 0 {
			proxy["grpc-opts"] = grpcOpts
		}
	case "http", "h2":
		proxy["network"] = "http"
		httpOpts := map[string]any{}
		if path := strings.TrimSpace(ValueToString(transport["Path"])); path != "" {
			httpOpts["path"] = []string{path}
		}
		if host := strings.TrimSpace(ValueToString(transport["Host"])); host != "" {
			httpOpts["headers"] = map[string][]string{"Host": {host}}
		}
		if len(httpOpts) > 0 {
			httpOpts["method"] = "GET"
			proxy["http-opts"] = httpOpts
		}
	case "xhttp":
		proxy["network"] = "xhttp"
		xhttpOpts := map[string]any{}
		if path := strings.TrimSpace(ValueToString(transport["Path"])); path != "" {
			xhttpOpts["path"] = path
		}
		if host := strings.TrimSpace(ValueToString(transport["Host"])); host != "" {
			xhttpOpts["host"] = host
		}
		if mode := strings.TrimSpace(ValueToString(transport["XhttpMode"])); mode != "" {
			xhttpOpts["mode"] = mode
		}
		if headers := parseJSONMapString(ValueToString(transport["XhttpExtra"])); len(headers) > 0 {
			xhttpOpts["extra"] = headers
		}
		if len(xhttpOpts) > 0 {
			proxy["xhttp-opts"] = xhttpOpts
		}
	default:
		proxy["network"] = network
	}
}

func applyImportedTLSFields(proxy map[string]any, obj map[string]any) {
	streamSecurity := strings.ToLower(strings.TrimSpace(ValueToString(obj["StreamSecurity"])))
	switch streamSecurity {
	case "tls":
		proxy["tls"] = true
	case "reality":
		proxy["tls"] = true
		proxy["reality-opts"] = map[string]any{
			"public-key": strings.TrimSpace(ValueToString(obj["PublicKey"])),
			"short-id":   strings.TrimSpace(ValueToString(obj["ShortId"])),
		}
	}
}

func buildImportedNodeFromProxyMap(proxy map[string]any) (exitpool.Node, bool) {
	if len(proxy) == 0 {
		return exitpool.Node{}, false
	}
	raw := proxyMapToURI(proxy)
	if raw == "" {
		return exitpool.Node{}, false
	}
	return ParseImportedNodeLine(raw)
}

func buildImportedNodeFromMap(obj map[string]any) (exitpool.Node, bool) {
	if proxy := buildImportedProxyFromV2RayNProfile(obj); len(proxy) > 0 {
		return buildImportedNodeFromProxyMap(proxy)
	}
	if proxy := buildImportedProxyFromV2RayOutbound(obj); len(proxy) > 0 {
		return buildImportedNodeFromProxyMap(proxy)
	}
	if proxy := buildImportedProxyFromSIP008(obj); len(proxy) > 0 {
		return buildImportedNodeFromProxyMap(proxy)
	}
	if looksLikeClashProxyMap(obj) {
		return buildClashNode(obj)
	}
	return exitpool.Node{}, false
}

func buildImportedNodesFromSlice(items []any) []exitpool.Node {
	imported := make([]exitpool.Node, 0, len(items))
	for _, item := range items {
		obj := mapValue(item)
		if len(obj) == 0 {
			continue
		}
		if node, ok2 := buildImportedNodeFromMap(obj); ok2 {
			imported = append(imported, node)
		}
	}
	return imported
}

func ParseJSONImportedNodes(text string) []exitpool.Node {
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	var raw any
	if err := json.Unmarshal([]byte(text), &raw); err != nil {
		return nil
	}

	normalized := normalizeYAMLValue(raw)
	if obj, ok := normalized.(map[string]any); ok {
		if proxies := buildImportedNodesFromSlice(sliceValue(obj["proxies"])); len(proxies) > 0 {
			return proxies
		}
		if outbounds := buildImportedNodesFromSlice(sliceValue(obj["outbounds"])); len(outbounds) > 0 {
			return outbounds
		}
		if servers := buildImportedNodesFromSlice(sliceValue(obj["servers"])); len(servers) > 0 {
			return servers
		}
		if node, ok2 := buildImportedNodeFromMap(obj); ok2 {
			return []exitpool.Node{node}
		}
		return nil
	}
	if items, ok := normalized.([]any); ok {
		return buildImportedNodesFromSlice(items)
	}
	return nil
}
