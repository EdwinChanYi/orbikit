package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

func (r *runner) caseAdmin() {
	code, body, _ := r.do(http.MethodPost, r.admin+"/api/login", map[string]string{"password": "wrong-password"}, nil)
	if code == http.StatusUnauthorized {
		r.pass("ADM-01", "错误口令返回 401")
	} else {
		r.fail("ADM-01", fmt.Sprintf("状态 %d %s", code, trim(body)))
	}
	code, body, session := r.do(http.MethodPost, r.admin+"/api/login", map[string]string{"password": r.password}, nil)
	if code != http.StatusOK && code != http.StatusNoContent || session == nil {
		r.fail("ADM-02", fmt.Sprintf("登录状态 %d %s", code, trim(body)))
		return
	}
	code, body, _ = r.do(http.MethodGet, r.admin+"/api/me", nil, session)
	if code == http.StatusOK && contains(body, "admin") {
		r.pass("ADM-02", "登录后 /api/me 返回当前操作者")
	} else {
		r.fail("ADM-02", fmt.Sprintf("/api/me %d %s", code, trim(body)))
		return
	}
	for _, path := range []string{"/api/overview", "/api/cluster", "/api/config/source", "/api/releases?service=core"} {
		code, _, _ = r.do(http.MethodGet, r.admin+path, nil, nil)
		if code != http.StatusUnauthorized {
			r.fail("ADM-03", path+" 未登录状态 "+fmt.Sprint(code))
			return
		}
	}
	r.pass("ADM-03", "未登录访问总览、集群、配置和发布都被拒绝")

	code, _, _ = r.do(http.MethodPost, r.admin+"/api/logout", nil, session)
	if code != http.StatusOK && code != http.StatusNoContent {
		r.fail("ADM-04", fmt.Sprintf("登出状态 %d", code))
		return
	}
	code, _, _ = r.do(http.MethodGet, r.admin+"/api/me", nil, session)
	if code == http.StatusUnauthorized {
		r.pass("ADM-04", "登出后会话失效")
	} else {
		r.fail("ADM-04", fmt.Sprintf("登出后 /api/me 状态 %d", code))
	}
	_, _, session = r.do(http.MethodPost, r.admin+"/api/login", map[string]string{"password": r.password}, nil)
	if session == nil {
		r.fail("ADM-05", "重新登录失败")
		return
	}

	code, body, _ = r.do(http.MethodGet, r.admin+"/api/overview", nil, session)
	var overview struct {
		Services []struct {
			Name      string `json:"name"`
			Container string `json:"container"`
		} `json:"services"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &overview) != nil {
		r.fail("ADM-05", fmt.Sprintf("总览 %d %s", code, trim(body)))
	} else {
		want := map[string]bool{"core": false, "chatroom": false, "gateway": false, "webhook": false}
		for _, svc := range overview.Services {
			if _, ok := want[svc.Name]; ok && svc.Container == "running" {
				want[svc.Name] = true
			}
		}
		missing := false
		for name, ok := range want {
			if !ok {
				missing = true
				r.fail("ADM-05", name+" 不在总览或容器不是 running")
			}
		}
		if !missing {
			r.pass("ADM-05", "总览包含 core、chatroom、gateway、webhook，容器都在运行。admin 和 builder 不在这张表里")
		}
	}

	code, body, _ = r.do(http.MethodGet, r.admin+"/api/cluster", nil, session)
	var cluster struct {
		Status    string `json:"status"`
		Instances []struct {
			Service string `json:"service"`
		} `json:"instances"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &cluster) != nil || cluster.Status != "ok" {
		r.fail("ADM-06", fmt.Sprintf("集群 %d %s", code, trim(body)))
	} else {
		seen := map[string]bool{}
		for _, inst := range cluster.Instances {
			seen[inst.Service] = true
		}
		if seen["core"] && seen["chatroom"] && seen["webhook"] {
			r.pass("ADM-06", fmt.Sprintf("etcd 状态正常，已注册 %d 个实例", len(cluster.Instances)))
		} else {
			r.fail("ADM-06", "注册实例里缺少 core、chatroom 或 webhook")
		}
	}

	code, body, _ = r.do(http.MethodGet, r.admin+"/api/config/source", nil, session)
	if code == http.StatusOK && contains(body, `"source":"nacos"`) {
		r.pass("ADM-07", "配置来源是 Nacos")
	} else {
		r.fail("ADM-07", fmt.Sprintf("source %d %s", code, trim(body)))
	}

	r.configRoundTrip(session)

	code, body, _ = r.do(http.MethodGet, r.admin+"/api/config/not-a-service", nil, session)
	if code == http.StatusBadRequest {
		r.pass("ADM-09", "非法服务名被拒绝")
	} else {
		r.fail("ADM-09", fmt.Sprintf("非法服务名状态 %d %s", code, trim(body)))
	}
	code, _, _ = r.do(http.MethodPut, r.admin+"/api/config/admin", "not-json", session)
	if code == http.StatusBadRequest {
		r.pass("ADM-09", "非法 JSON 被拒绝")
	} else {
		r.fail("ADM-09", fmt.Sprintf("非法 JSON 状态 %d", code))
	}

	pagesOK := true
	for _, path := range []string{"/", "/login", "/config", "/cluster", "/release"} {
		code, body, _ = r.do(http.MethodGet, r.admin+path, nil, session)
		if code != http.StatusOK || !contains(body, "<div") && !contains(body, "<html") {
			pagesOK = false
			r.fail("ADM-10", fmt.Sprintf("%s 状态 %d", path, code))
		}
	}
	if pagesOK {
		r.pass("ADM-10", "登录页、总览、配置、集群和发布页面都返回前端页面")
	}
}

func (r *runner) configRoundTrip(session *http.Cookie) {
	code, body, _ := r.do(http.MethodGet, r.admin+"/api/config/admin", nil, session)
	var current struct {
		Content string `json:"content"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &current) != nil || current.Content == "" {
		r.fail("ADM-08", fmt.Sprintf("读取配置 %d %s", code, trim(body)))
		return
	}
	original := current.Content
	if !strings.Contains(original, "level: info") {
		r.fail("ADM-08", "当前配置里没有 level: info，跳过热更新以免改错文件")
		return
	}
	next := strings.Replace(original, "level: info", "level: debug", 1)
	code, body, _ = r.do(http.MethodPut, r.admin+"/api/config/admin", map[string]string{"content": next}, session)
	if code != http.StatusNoContent {
		r.fail("ADM-08", fmt.Sprintf("发布配置 %d %s", code, trim(body)))
		return
	}
	defer func() {
		_, _, _ = r.do(http.MethodPut, r.admin+"/api/config/admin", map[string]string{"content": original}, session)
	}()
	code, body, _ = r.do(http.MethodGet, r.admin+"/api/config/admin/history", nil, session)
	var history struct {
		Revisions []struct {
			ID      string `json:"id"`
			Content string `json:"content"`
		} `json:"revisions"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &history) != nil || len(history.Revisions) == 0 {
		r.fail("ADM-08", fmt.Sprintf("历史 %d %s", code, trim(body)))
		return
	}
	var rollbackID string
	for _, rev := range history.Revisions {
		if rev.Content == original {
			rollbackID = rev.ID
			break
		}
	}
	if rollbackID == "" {
		r.fail("ADM-08", "历史里没有改动前的版本")
		return
	}
	code, body, _ = r.do(http.MethodPost, r.admin+"/api/config/admin/rollback", map[string]string{"id": rollbackID}, session)
	if code != http.StatusNoContent {
		r.fail("ADM-08", fmt.Sprintf("回滚 %d %s", code, trim(body)))
		return
	}
	code, body, _ = r.do(http.MethodGet, r.admin+"/api/config/admin", nil, session)
	var after struct {
		Content string `json:"content"`
	}
	_ = json.Unmarshal(body, &after)
	if after.Content != original {
		r.fail("ADM-08", "回滚后的内容与原来不一致")
		return
	}
	betaCode, betaBody, _ := r.do(http.MethodPut, r.admin+"/api/config/admin/beta", map[string]any{"content": original, "ips": []string{"not-an-ip"}}, session)
	listenCode, _, _ := r.do(http.MethodGet, r.admin+"/api/config/admin/listeners", nil, session)
	if betaCode == http.StatusBadRequest && listenCode == http.StatusOK {
		r.pass("ADM-08", "配置可发布、可看历史、可回滚；非法灰度地址被拒绝，监听者可查询")
	} else {
		r.fail("ADM-08", fmt.Sprintf("灰度 %d %s 监听 %d", betaCode, trim(betaBody), listenCode))
	}
}

func (r *runner) caseBuilder() {
	for _, name := range []string{"core", "chatroom", "gateway", "webhook"} {
		code, body, _ := r.do(http.MethodGet, r.builder+"/v1/containers/"+name, nil, nil)
		var info struct {
			Running bool   `json:"running"`
			Status  string `json:"status"`
		}
		if code != http.StatusOK || json.Unmarshal(body, &info) != nil || !info.Running {
			r.fail("BLD-01", fmt.Sprintf("%s %d %s", name, code, trim(body)))
			return
		}
	}
	code, body, _ := r.do(http.MethodGet, r.builder+"/v1/containers/admin", nil, nil)
	if code == http.StatusBadRequest {
		r.pass("BLD-01", "core、chatroom、gateway、webhook 在运行。admin 不在容器查询白名单，返回 400")
	} else {
		r.fail("BLD-01", fmt.Sprintf("admin 查询状态 %d %s", code, trim(body)))
	}
	code, body, _ = r.do(http.MethodGet, r.builder+"/v1/containers/not-a-service", nil, nil)
	if code == http.StatusBadRequest && contains(body, "允许") {
		r.pass("BLD-02", "非法服务名被拒绝")
	} else {
		r.fail("BLD-02", fmt.Sprintf("%d %s", code, trim(body)))
	}
	code, body, _ = r.do(http.MethodGet, r.builder+"/v1/releases/ready", nil, nil)
	var ready struct {
		Configured bool `json:"configured"`
	}
	if code != http.StatusOK || json.Unmarshal(body, &ready) != nil {
		r.fail("BLD-03", fmt.Sprintf("%d %s", code, trim(body)))
	} else if ready.Configured {
		r.pass("BLD-03", "发布凭据已配置")
	} else {
		r.fail("BLD-03", "发布凭据未配置，真实发布无法开始")
	}

	_, _, session := r.do(http.MethodPost, r.admin+"/api/login", map[string]string{"password": r.password}, nil)
	if session == nil {
		r.fail("BLD-04", "登录失败")
		return
	}
	code, body, _ = r.do(http.MethodPost, r.admin+"/api/releases", map[string]string{"service": "not-a-service", "ref": "v1.0.0"}, session)
	if code != http.StatusBadRequest {
		r.fail("BLD-04", fmt.Sprintf("非法服务名 %d %s", code, trim(body)))
	} else {
		code, body, _ = r.do(http.MethodPost, r.admin+"/api/releases", map[string]string{"service": r.release, "ref": "abc"}, session)
		if code == http.StatusBadRequest {
			r.pass("BLD-04", "非法服务名和短 SHA 都被拒绝")
		} else {
			r.fail("BLD-04", fmt.Sprintf("短 SHA %d %s", code, trim(body)))
		}
	}
	code, body, _ = r.do(http.MethodGet, r.admin+"/api/releases?service="+r.release, nil, session)
	if code != http.StatusOK {
		r.fail("BLD-06", fmt.Sprintf("查询发布记录 %d %s", code, trim(body)))
	} else {
		r.pass("BLD-06", "能读到发布记录列表")
	}

	if r.ref == "" {
		r.fail("BLD-07", "没有提供可发布的 tag 或完整 SHA")
		return
	}
	code, body, _ = r.do(http.MethodPost, r.admin+"/api/releases", map[string]string{"service": r.release, "ref": r.ref}, session)
	var accepted struct {
		ID uint64 `json:"id"`
	}
	if code != http.StatusAccepted || json.Unmarshal(body, &accepted) != nil || accepted.ID == 0 {
		r.fail("BLD-07", fmt.Sprintf("发起发布 %d %s", code, trim(body)))
		return
	}
	code, body, _ = r.do(http.MethodPost, r.admin+"/api/releases", map[string]string{"service": "core", "ref": r.ref}, session)
	if code == http.StatusConflict {
		r.pass("BLD-05", "已有发布进行时再次发布返回 409")
	} else {
		r.fail("BLD-05", fmt.Sprintf("并发发布状态 %d %s", code, trim(body)))
	}
	task := r.waitRelease(session, accepted.ID)
	if task.Status == "succeeded" {
		r.pass("BLD-07", fmt.Sprintf("已用 %s 发布 %s，任务 %d", r.ref, r.release, accepted.ID))
	} else {
		r.fail("BLD-07", fmt.Sprintf("状态 %s %s %s", task.Status, task.Error, tail(task.Log)))
	}
	code, body, _ = r.do(http.MethodPost, r.admin+"/api/releases/"+fmt.Sprint(accepted.ID)+"/recreate", nil, session)
	if task.Status == "failed" && task.ImageID != "" && code == http.StatusAccepted {
		r.pass("BLD-06", "失败且已有镜像的记录可以重建")
	} else if code == http.StatusBadRequest {
		r.pass("BLD-06", "当前记录不满足重建条件时被拒绝")
	} else {
		r.fail("BLD-06", fmt.Sprintf("重建状态 %d %s", code, trim(body)))
	}
}

type releaseTask struct {
	Status  string `json:"status"`
	Error   string `json:"error"`
	Log     string `json:"log"`
	ImageID string `json:"image_id"`
}

func (r *runner) waitRelease(session *http.Cookie, id uint64) releaseTask {
	deadline := time.Now().Add(30 * time.Minute)
	var task releaseTask
	for time.Now().Before(deadline) {
		code, body, _ := r.do(http.MethodGet, r.admin+"/api/releases/"+fmt.Sprint(id), nil, session)
		if code == http.StatusOK && json.Unmarshal(body, &task) == nil {
			if task.Status == "succeeded" || task.Status == "failed" {
				return task
			}
		}
		time.Sleep(5 * time.Second)
	}
	task.Status = "timeout"
	task.Error = "30 分钟内没有结束"
	return task
}

func trim(b []byte) string {
	s := strings.TrimSpace(string(b))
	if len(s) > 240 {
		return s[:240]
	}
	return s
}

func tail(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > 400 {
		return s[len(s)-400:]
	}
	return s
}
