package admin

import (
	"net/http"
)

// overviewHandler serves the console overview page: status breakdown with
// zero-filled unified statuses, 24 hourly buckets oldest first, delivery
// states, per-provider reliability and the needs-attention block.
func (s *Server) overview(w http.ResponseWriter, r *http.Request) {
	env, ok := resolveEnvironment(w, r.URL.Query().Get("env"))
	if !ok {
		return
	}

	stats, err := s.stats.Overview(r.Context(), env)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "internal_error", "cannot load overview")
		return
	}

	statuses := make([]map[string]any, 0, len(stats.Statuses))
	for _, row := range stats.Statuses {
		statuses = append(statuses, map[string]any{
			"status": string(row.Status),
			"count":  row.Count,
		})
	}

	unknown, _ := s.transactions.List(r.Context(), transactionFilter(env, map[string]string{
		"status": "unknown",
		"limit":  "10",
	}))
	unknownRows := make([]map[string]any, 0, len(unknown))
	for _, tx := range unknown {
		unknownRows = append(unknownRows, map[string]any{
			"id":         tx.ID,
			"reference":  tx.ReferenceID,
			"provider":   tx.ProviderName,
			"created_at": tx.CreatedAt,
		})
	}

	providers := make([]map[string]any, 0, len(stats.Providers))
	for _, row := range stats.Providers {
		providers = append(providers, map[string]any{
			"provider": row.Provider,
			"payments": row.Payments,
			"failed":   row.Failed,
		})
	}

	writeData(w, http.StatusOK, map[string]any{
		"total_transactions": stats.TotalTransactions,
		"statuses":           statuses,
		"created_per_hour":   stats.CreatedPerHour,
		"failed_per_hour":    stats.FailedPerHour,
		"deliveries": map[string]any{
			"delivered": stats.DeliveredDeliveries,
			"pending":   stats.PendingDeliveries,
			"failed":    stats.FailedDeliveries,
		},
		"providers": providers,
		"attention": map[string]any{
			"failed_deliveries":    stats.FailedDeliveries,
			"unknown_transactions": unknownRows,
		},
	}, map[string]any{"environment": env})
}
