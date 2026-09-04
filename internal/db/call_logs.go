package db

import (
	"fmt"
	"log"
	"strings"
	"time"
)

type CallLog struct {
	ID               int64   `json:"id"`
	RequestID        string  `json:"request_id"`
	KeyName          string  `json:"key_name"`
	KeyPrefix        string  `json:"key_prefix"`
	Model            string  `json:"model"`
	IsStream         bool    `json:"is_stream"`
	StatusCode       int     `json:"status_code"`
	FirstTokenMs     int64   `json:"first_token_ms"`
	TotalDurationMs  int64   `json:"total_duration_ms"`
	PromptTokens     int     `json:"prompt_tokens"`
	CompletionTokens int     `json:"completion_tokens"`
	TotalTokens      int     `json:"total_tokens"`
	WinnerNode       string  `json:"winner_node"`
	ErrorMessage     string  `json:"error_message"`
	CreatedAt        int64   `json:"created_at"`
	CostUSD          float64 `json:"cost_usd"`
}

type CallLogQuery struct {
	KeyName, Model, Status string
	StartTime, EndTime     int64
	Page, PageSize         int
}
type CallLogStats struct {
	TotalRequests     int64   `json:"total_requests"`
	SuccessRequests   int64   `json:"success_requests"`
	ErrorRequests     int64   `json:"error_requests"`
	TotalPromptTokens int64   `json:"total_prompt_tokens"`
	TotalComplTokens  int64   `json:"total_compl_tokens"`
	TotalTokens       int64   `json:"total_tokens"`
	AvgFirstTokenMs   float64 `json:"avg_first_token_ms"`
	AvgDurationMs     float64 `json:"avg_duration_ms"`
	EstimatedCostUSD  float64 `json:"estimated_cost_usd"`
}
type CallLogResult struct {
	Items           []CallLog    `json:"items"`
	Total           int64        `json:"total"`
	Page            int          `json:"page"`
	PageSize        int          `json:"page_size"`
	Stats           CallLogStats `json:"stats"`
	AvailableKeys   []string     `json:"available_keys"`
	AvailableModels []string     `json:"available_models"`
}

func RecordCallLog(l *CallLog) {
	if GlobalDB == nil || l == nil {
		return
	}
	if l.CreatedAt == 0 {
		l.CreatedAt = time.Now().Unix()
	}
	item := *l
	_, err := GlobalDB.Exec(`INSERT INTO call_logs
		(request_id,key_name,key_prefix,model,is_stream,status_code,first_token_ms,total_duration_ms,prompt_tokens,completion_tokens,total_tokens,winner_node,error_message,created_at)
		VALUES (?,?,?,?,?,?,?,?,?,?,?,?,?,?)`, item.RequestID, item.KeyName, item.KeyPrefix, item.Model, item.IsStream, item.StatusCode, item.FirstTokenMs, item.TotalDurationMs, item.PromptTokens, item.CompletionTokens, item.TotalTokens, item.WinnerNode, item.ErrorMessage, item.CreatedAt)
	if err != nil {
		log.Printf("[DB] RecordCallLog failed: %v", err)
	}
}

func QueryCallLogs(q CallLogQuery) (*CallLogResult, error) {
	if GlobalDB == nil {
		return nil, fmt.Errorf("database not initialized")
	}
	if q.Page <= 0 {
		q.Page = 1
	}
	if q.PageSize <= 0 || q.PageSize > 100 {
		q.PageSize = 20
	}
	where := []string{}
	args := []any{}
	if q.KeyName != "" && q.KeyName != "all" {
		where = append(where, "key_name = ?")
		args = append(args, q.KeyName)
	}
	if q.Model != "" && q.Model != "all" {
		where = append(where, "model = ?")
		args = append(args, q.Model)
	}
	if q.StartTime > 0 {
		where = append(where, "created_at >= ?")
		args = append(args, q.StartTime)
	}
	if q.EndTime > 0 {
		where = append(where, "created_at <= ?")
		args = append(args, q.EndTime)
	}
	switch q.Status {
	case "success":
		where = append(where, "status_code = 200")
	case "error":
		where = append(where, "status_code != 200")
	case "stream":
		where = append(where, "is_stream = 1")
	}
	clause := ""
	if len(where) > 0 {
		clause = " WHERE " + strings.Join(where, " AND ")
	}
	var out CallLogResult
	out.Page, out.PageSize = q.Page, q.PageSize
	statsSQL := `SELECT COUNT(*),COALESCE(SUM(CASE WHEN status_code=200 THEN 1 ELSE 0 END),0),COALESCE(SUM(CASE WHEN status_code!=200 THEN 1 ELSE 0 END),0),COALESCE(SUM(prompt_tokens),0),COALESCE(SUM(completion_tokens),0),COALESCE(SUM(total_tokens),0),COALESCE(AVG(NULLIF(first_token_ms,0)),0),COALESCE(AVG(NULLIF(total_duration_ms,0)),0) FROM call_logs` + clause
	if err := GlobalDB.QueryRow(statsSQL, args...).Scan(&out.Stats.TotalRequests, &out.Stats.SuccessRequests, &out.Stats.ErrorRequests, &out.Stats.TotalPromptTokens, &out.Stats.TotalComplTokens, &out.Stats.TotalTokens, &out.Stats.AvgFirstTokenMs, &out.Stats.AvgDurationMs); err != nil {
		return nil, err
	}
	out.Stats.EstimatedCostUSD = (float64(out.Stats.TotalPromptTokens)*0.10 + float64(out.Stats.TotalComplTokens)*0.40) / 1000000
	listArgs := append(append([]any{}, args...), q.PageSize, (q.Page-1)*q.PageSize)
	rows, err := GlobalDB.Query(`SELECT id,request_id,key_name,key_prefix,model,is_stream,status_code,first_token_ms,total_duration_ms,prompt_tokens,completion_tokens,total_tokens,winner_node,error_message,created_at FROM call_logs`+clause+` ORDER BY id DESC LIMIT ? OFFSET ?`, listArgs...)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item CallLog
		var stream int
		if err := rows.Scan(&item.ID, &item.RequestID, &item.KeyName, &item.KeyPrefix, &item.Model, &stream, &item.StatusCode, &item.FirstTokenMs, &item.TotalDurationMs, &item.PromptTokens, &item.CompletionTokens, &item.TotalTokens, &item.WinnerNode, &item.ErrorMessage, &item.CreatedAt); err != nil {
			return nil, err
		}
		item.IsStream = stream != 0
		item.CostUSD = (float64(item.PromptTokens)*0.10 + float64(item.CompletionTokens)*0.40) / 1000000
		out.Items = append(out.Items, item)
	}
	out.Total = out.Stats.TotalRequests
	for _, dst := range []struct {
		query  string
		target *[]string
	}{{"SELECT DISTINCT key_name FROM call_logs WHERE key_name != '' ORDER BY key_name", &out.AvailableKeys}, {"SELECT DISTINCT model FROM call_logs WHERE model != '' ORDER BY model", &out.AvailableModels}} {
		rs, e := GlobalDB.Query(dst.query)
		if e != nil {
			continue
		}
		for rs.Next() {
			var v string
			if rs.Scan(&v) == nil {
				*dst.target = append(*dst.target, v)
			}
		}
		rs.Close()
	}
	return &out, nil
}

func ClearCallLogs() error {
	if GlobalDB == nil {
		return fmt.Errorf("database not initialized")
	}
	_, err := GlobalDB.Exec("DELETE FROM call_logs")
	return err
}
