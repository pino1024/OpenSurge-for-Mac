package controlapi

import (
	"context"
	"net/http"

	"open-mihomo-gateway/internal/config"
	"open-mihomo-gateway/internal/mihomo"
)

func (s *Server) handleProxyEnabled(w http.ResponseWriter, r *http.Request) {
	enabled, err := s.store.ProxyEnabled()
	if err != nil {
		writeError(w, http.StatusInternalServerError, "proxy_enabled_read_failed", err.Error())
		return
	}
	if r.Method == http.MethodPost {
		var request ProxyEnabledRequest
		if decodeErr := decodeJSON(r, &request, 64<<10); decodeErr != nil {
			writeError(w, http.StatusBadRequest, "invalid_request", decodeErr.Error())
			return
		}
		if request.Enabled == nil {
			writeError(w, http.StatusBadRequest, "invalid_request", "enabled is required")
			return
		}
		enabled = *request.Enabled
		if err := s.store.SaveProxyEnabled(enabled); err != nil {
			writeError(w, http.StatusInternalServerError, "proxy_enabled_write_failed", err.Error())
			return
		}
	}
	writeJSON(w, http.StatusOK, s.proxyEnabledResponse(r.Context(), enabled, r.Method == http.MethodPost))
}

func (s *Server) proxyEnabledResponse(ctx context.Context, enabled bool, apply bool) ProxyEnabledResponse {
	response := ProxyEnabledResponse{
		SchemaVersion: SchemaVersion,
		Enabled:       enabled,
		Mode:          mihomo.TrafficModeForProxyEnabled(enabled),
	}
	cfg, err := config.LoadRuntime(s.configPath)
	if err != nil {
		if apply {
			response.Warning = "已记住开关，启动网关后对所有设备生效。"
		}
		return response
	}
	if apply {
		live, warning, applyErr := s.applyProxyMode(ctx, cfg, enabled, true)
		response.Live = live
		response.Warning = warning
		if applyErr != nil {
			response.Warning = applyErr.Error()
		}
		return response
	}
	mode, err := s.fetchTrafficMode(ctx, cfg)
	if err != nil {
		return response
	}
	response.Live = true
	response.Mode = mode
	response.Enabled = mihomo.ProxyEnabledFromTrafficMode(mode)
	return response
}

func (s *Server) applyStoredProxyMode(ctx context.Context) {
	enabled, err := s.store.ProxyEnabled()
	if err != nil {
		return
	}
	cfg, err := config.LoadRuntime(s.configPath)
	if err != nil {
		return
	}
	_, _, _ = s.applyProxyMode(ctx, cfg, enabled, !enabled)
}

func (s *Server) applyProxyMode(ctx context.Context, cfg config.Config, enabled bool, closeConnections bool) (live bool, warning string, err error) {
	mode := mihomo.TrafficModeForProxyEnabled(enabled)
	if patchErr := s.patchTrafficMode(ctx, cfg, mode); patchErr != nil {
		if _, liveErr := s.fetchTrafficMode(ctx, cfg); liveErr != nil {
			return false, "已记住开关，启动网关后对所有设备生效。", nil
		}
		return false, "", patchErr
	}
	if closeConnections {
		if closeErr := s.closeAllConnections(ctx, cfg); closeErr != nil {
			return true, "代理开关已生效，但未能断开旧连接。", nil
		}
	}
	return true, "", nil
}
