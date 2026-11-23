package uploader

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// TestSubmitOfferFormSuccess 验证候选提交：隐藏字段被合并、urlencoded 提交、候选链接被解析。
func TestSubmitOfferFormSuccess(t *testing.T) {
	var captured url.Values
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/offer.php"):
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<html><body><form action="takeoffer.php" method="post">
<input type="hidden" name="csrf_token" value="tok-123"/>
<input type="text" name="name"/>
</form></body></html>`)
		case strings.HasSuffix(r.URL.Path, "/takeoffer.php"):
			if err := r.ParseForm(); err != nil {
				t.Errorf("解析表单失败: %v", err)
			}
			captured = r.PostForm
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<html><head><title>TTG :: 申请成功！</title></head><body>
<p>您的候选已提交，<a href="/offers.php?id=7788">点击查看</a></p></body></html>`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	result, err := TrySubmitOfferForm(
		srv.URL+"/offer.php",
		srv.URL+"/takeoffer.php",
		srv.URL,
		"uid=1; pass=x",
		map[string]string{
			"name":        "Some.Movie.2024.1080p.BluRay.x264-GROUP",
			"msg":         "简介正文",
			"type":        "53",
			"seeding_env": "7x24小时在线",
		},
		"",
	)
	if err != nil {
		t.Fatalf("TrySubmitOfferForm 失败: %v", err)
	}
	if captured == nil {
		t.Fatal("未捕获到提交字段")
	}
	// 隐藏字段应被合并（业务字段优先后写，不覆盖 csrf）
	if captured.Get("csrf_token") != "tok-123" {
		t.Errorf("csrf_token = %q, want tok-123", captured.Get("csrf_token"))
	}
	if captured.Get("name") != "Some.Movie.2024.1080p.BluRay.x264-GROUP" {
		t.Errorf("name = %q", captured.Get("name"))
	}
	if captured.Get("seeding_env") != "7x24小时在线" {
		t.Errorf("seeding_env = %q", captured.Get("seeding_env"))
	}
	if want := srv.URL + "/offers.php?id=7788"; result.OfferURL != want {
		t.Errorf("OfferURL = %q, want %q", result.OfferURL, want)
	}
}

// TestSubmitOfferFormRejected 验证候选被拒时，错误信息带上站点给出的具体原因。
func TestSubmitOfferFormRejected(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/takeoffer.php") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<html><head><title>TTG :: 申请失败！</title></head><body>
<h1>申请失败！</h1><p>don't leave any fields blank</p></body></html>`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	_, err := TrySubmitOfferForm(
		srv.URL+"/offer.php",
		srv.URL+"/takeoffer.php",
		srv.URL,
		"uid=1; pass=x",
		map[string]string{"name": "Some.Movie.2024.1080p-GROUP"},
		"",
	)
	if err == nil {
		t.Fatal("期望返回错误，实际成功")
	}
	if !strings.Contains(err.Error(), "don't leave any fields blank") {
		t.Errorf("错误信息应包含站点原因, got %v", err)
	}
}

// TestSubmitOfferFormSuccessWithoutLink 验证站点只提示成功、不给链接时按成功处理（OfferURL 为空）。
func TestSubmitOfferFormSuccessWithoutLink(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/takeoffer.php") {
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<html><head><title>TTG :: 申请成功！</title></head><body>
<p>候选提交成功，等待审核。</p></body></html>`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	result, err := TrySubmitOfferForm(
		srv.URL+"/offer.php",
		srv.URL+"/takeoffer.php",
		srv.URL,
		"uid=1; pass=x",
		map[string]string{"name": "Some.Movie.2024.1080p-GROUP"},
		"",
	)
	if err != nil {
		t.Fatalf("TrySubmitOfferForm 失败: %v", err)
	}
	if result.OfferURL != "" {
		t.Errorf("OfferURL = %q, want 空", result.OfferURL)
	}
}

// TestSubmitOfferFormLocationLink 验证提交成功只回 302 时，从 Location 里取候选链接。
// TTG 的真实形态是 Location: /offer.php?vtype=for&id=N（offer.php 单数），
// 早期实现只解析 body，于是「提交成功」被当成「没拿到链接」，链接还被硬编码成必然 404 的 offers.php。
func TestSubmitOfferFormLocationLink(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/takeoffer.php") {
			w.Header().Set("Location", "/offer.php?vtype=for&id=3171")
			w.WriteHeader(http.StatusFound)
			fmt.Fprint(w, `<html><body>页脚</body></html>`)
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	result, err := TrySubmitOfferForm(
		srv.URL+"/offer.php",
		srv.URL+"/takeoffer.php",
		srv.URL,
		"uid=1; pass=x",
		map[string]string{"name": "Ao no Hako S01 2026 1080p NF WEB-DL H.264 DDP 5.1-COLAWEB"},
		"",
	)
	if err != nil {
		t.Fatalf("TrySubmitOfferForm 失败: %v", err)
	}
	want := srv.URL + "/offer.php?vtype=for&id=3171"
	if result.OfferURL != want {
		t.Fatalf("OfferURL = %q, want %q", result.OfferURL, want)
	}
}

// TestSubmitOfferFormResolveFromListPage 验证 302 只跳到候选区列表页时，按名称回查候选链接。
// 列表页里同时存在别人的候选，必须按行匹配名称，不能抓到第一条链接。
func TestSubmitOfferFormResolveFromListPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/takeoffer.php"):
			w.Header().Set("Location", "/viewoffers.php")
			w.WriteHeader(http.StatusFound)
			fmt.Fprint(w, `<html><body>页脚</body></html>`)
		case strings.HasSuffix(r.URL.Path, "/viewoffers.php"):
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<html><body><table>
<tr><td><a href="offer.php?vtype=against&id=3170">反对</a></td><td>Some.Other.Movie.2024.1080p-GROUP</td></tr>
<tr><td><a href="offer.php?vtype=against&id=3171">反对</a></td><td>Ao no Hako S01 2026 1080p NF WEB-DL H.264 DDP 5.1-COLAWEB [青春之箱</td></tr>
</table></body></html>`)
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer srv.Close()

	result, err := TrySubmitOfferForm(
		srv.URL+"/offer.php",
		srv.URL+"/takeoffer.php",
		srv.URL,
		"uid=1; pass=x",
		map[string]string{"name": "Ao no Hako S01 2026 1080p NF WEB-DL H.264 DDP 5.1-COLAWEB [青春之箱 | 日语 | 2026]"},
		"viewoffers.php",
	)
	if err != nil {
		t.Fatalf("TrySubmitOfferForm 失败: %v", err)
	}
	if !strings.Contains(result.OfferURL, "id=3171") {
		t.Fatalf("OfferURL = %q, 应回查到 id=3171\n--- detail ---\n%s", result.OfferURL, result.DetailLog)
	}
	if !strings.Contains(result.OfferURL, "offer.php") {
		t.Fatalf("OfferURL = %q, 应保留站点真实路径 offer.php", result.OfferURL)
	}
}

// TestSubmitOfferFormCHDBitsErrorPage 验证彩虹岛形态的失败页能被识别：
// 页面不是 <h1>失败！</h1><p>原因</p>，而是 <h2>错误！</h2> + 紧随其后的 <td class="text">原因</td>。
// 早期只认 <p>，这类页面会被当成「200 且无错误提示」→ 误报提交成功。
func TestSubmitOfferFormCHDBitsErrorPage(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		if r.Method == http.MethodPost {
			fmt.Fprint(w, `<html><head><title>CHDBits :: 候选错误 - Powered by NexusPHP</title></head><body>
<table class="main"><tr><td class="embedded"><h2>错误！</h2>
<table width="100%" border="1"><tr><td class="text">你必须输入标题！</td></tr></table>
</td></tr></table></body></html>`)
			return
		}
		fmt.Fprint(w, `<html><body><form id="compose" action="?new_offer=1" method="post">
<input type="text" name="name" /><textarea name="body"></textarea></form></body></html>`)
	}))
	defer srv.Close()

	_, err := TrySubmitOfferForm(
		srv.URL+"/offers.php?add_offer=1",
		srv.URL+"/offers.php?new_offer=1",
		srv.URL,
		"c_secure_uid=1; c_secure_pass=x",
		map[string]string{"name": "", "body": "简介"},
		"offers.php",
	)
	if err == nil {
		t.Fatal("站点明确拒绝（错误！你必须输入标题！）时应返回错误")
	}
	if !strings.Contains(err.Error(), "你必须输入标题") {
		t.Fatalf("错误信息应带出站点原因, got %v", err)
	}
}

// TestSubmitOfferFormCHDBitsListResolve 验证「302 跳候选区列表页」的场景：
// 列表页里有别人几十条候选（相对链接 ?id=N&off_details=1），必须按名称挑出本次提交的那条，
// 不能把第一条链接当成自己的候选。
func TestSubmitOfferFormCHDBitsListResolve(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodPost:
			w.Header().Set("Location", "/offers.php")
			w.WriteHeader(http.StatusFound)
			fmt.Fprint(w, `<html><body>redirect</body></html>`)
		case strings.Contains(r.URL.RawQuery, "add_offer=1"):
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<html><body><form id="compose" action="?new_offer=1" method="post">
<input type="text" name="name" /></form></body></html>`)
		default:
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			fmt.Fprint(w, `<html><body><table>
<tr><td><a href="?id=57324&amp;off_details=1" title="Some.Other.Movie.2026.1080p-GROUP"><b>Some.Other.Movie.2026.1080p-GROUP</b></a></td></tr>
<tr><td><a href="?id=57325&amp;off_details=1" title="Coyote vs Acme 2026 GBR BluRay 1080p x264 DDP 5.1-CMCT"><b>Coyote vs Acme 2026 GBR BluRay 1080p x264 DDP 5.1-CMCT</b></a></td></tr>
<tr><td><a href="?id=57326&amp;off_details=1" title="The Shadow Boxing 1979 1080p BluRay x264 LPCM-YzYY"><b>The Shadow Boxing 1979 1080p BluRay x264 LPCM-YzYY</b></a></td></tr>
</table></body></html>`)
		}
	}))
	defer srv.Close()

	result, err := TrySubmitOfferForm(
		srv.URL+"/offers.php?add_offer=1",
		srv.URL+"/offers.php?new_offer=1",
		srv.URL,
		"c_secure_uid=1; c_secure_pass=x",
		map[string]string{"name": "The Shadow Boxing 1979 1080p BluRay x264 LPCM-YzYY", "body": "简介"},
		"offers.php",
	)
	if err != nil {
		t.Fatalf("TrySubmitOfferForm 失败: %v", err)
	}
	want := srv.URL + "/offers.php?id=57326&off_details=1"
	if result.OfferURL != want {
		t.Fatalf("OfferURL = %q, want %q\n--- detail ---\n%s", result.OfferURL, want, result.DetailLog)
	}
}
