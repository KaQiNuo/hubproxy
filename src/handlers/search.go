package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"hubproxy/config"
	"hubproxy/utils"
)

// SearchResult Docker Hub搜索结果
type SearchResult struct {
	Count    int          `json:"count"`
	Next     string       `json:"next"`
	Previous string       `json:"previous"`
	Results  []Repository `json:"results"`
}

// Repository 仓库信息
type Repository struct {
	Name          string `json:"repo_name"`
	Description   string `json:"short_description"`
	IsOfficial    bool   `json:"is_official"`
	IsAutomated   bool   `json:"is_automated"`
	StarCount     int    `json:"star_count"`
	PullCount     int    `json:"pull_count"`
	RepoOwner     string `json:"repo_owner"`
	LastUpdated   string `json:"last_updated"`
	Status        int    `json:"status"`
	Organization  string `json:"affiliation"`
	PullsLastWeek int    `json:"pulls_last_week"`
	Namespace     string `json:"namespace"`
	Source        string `json:"source"` // 新增字段：镜像源
}

// MultiSourceSearchResult 多源搜索结果
type MultiSourceSearchResult struct {
	Source    string       `json:"source"`
	Count     int          `json:"count"`
	Next      string       `json:"next"`
	Previous  string       `json:"previous"`
	Results   []Repository `json:"results"`
	Error     string       `json:"error,omitempty"`
}

// TagInfo 标签信息
type TagInfo struct {
	Name            string    `json:"name"`
	FullSize        int64     `json:"full_size"`
	LastUpdated     time.Time `json:"last_updated"`
	LastPusher      string    `json:"last_pusher"`
	Images          []Image   `json:"images"`
	Vulnerabilities struct {
		Critical int `json:"critical"`
		High     int `json:"high"`
		Medium   int `json:"medium"`
		Low      int `json:"low"`
		Unknown  int `json:"unknown"`
	} `json:"vulnerabilities"`
}

// Image 镜像信息
type Image struct {
	Architecture string `json:"architecture"`
	Features     string `json:"features"`
	Variant      string `json:"variant,omitempty"`
	Digest       string `json:"digest"`
	OS           string `json:"os"`
	OSFeatures   string `json:"os_features"`
	Size         int64  `json:"size"`
}

// TagPageResult 分页标签结果
type TagPageResult struct {
	Tags    []TagInfo `json:"tags"`
	HasMore bool      `json:"has_more"`
}

type cacheEntry struct {
	data      interface{}
	expiresAt time.Time
}

const (
	maxCacheSize       = 1000
	maxPaginationCache = 200
	cacheTTL           = 30 * time.Minute
	defaultPageSize    = 25
)

// getEnabledRegistries 获取所有启用的注册表
func getEnabledRegistries() map[string]config.RegistryMapping {
	cfg := config.GetConfig()
	enabledRegistries := make(map[string]config.RegistryMapping)

	for domain, registry := range cfg.Registries {
		if registry.Enabled {
			enabledRegistries[domain] = registry
		}
	}

	return enabledRegistries
}

type Cache struct {
	data    map[string]cacheEntry
	mu      sync.RWMutex
	maxSize int
}

var (
	searchCache = &Cache{
		data:    make(map[string]cacheEntry),
		maxSize: maxCacheSize,
	}
)

func (c *Cache) Get(key string) (interface{}, bool) {
	c.mu.RLock()
	entry, exists := c.data[key]
	c.mu.RUnlock()

	if !exists {
		return nil, false
	}

	if time.Now().After(entry.expiresAt) {
		c.mu.Lock()
		delete(c.data, key)
		c.mu.Unlock()
		return nil, false
	}

	return entry.data, true
}

func (c *Cache) Set(key string, data interface{}) {
	c.SetWithTTL(key, data, cacheTTL)
}

func (c *Cache) SetWithTTL(key string, data interface{}, ttl time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.data) >= c.maxSize {
		c.cleanupExpiredLocked()
	}

	c.data[key] = cacheEntry{
		data:      data,
		expiresAt: time.Now().Add(ttl),
	}
}

func (c *Cache) Cleanup() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.cleanupExpiredLocked()
}

func (c *Cache) cleanupExpiredLocked() {
	now := time.Now()
	for key, entry := range c.data {
		if now.After(entry.expiresAt) {
			delete(c.data, key)
		}
	}
}

func init() {
	go func() {
		ticker := time.NewTicker(5 * time.Minute)
		defer ticker.Stop()

		for range ticker.C {
			searchCache.Cleanup()
		}
	}()
}

// searchRegistryByDomain 按域名搜索指定注册表
func searchRegistryByDomain(ctx context.Context, domain, query string, page, pageSize int) (*SearchResult, error) {
	switch domain {
	case "docker.io":
		// Docker Hub 搜索
		return searchDockerHub(ctx, query, page, pageSize)
	case "ghcr.io":
		// GitHub Container Registry 搜索
		return searchGHCR(ctx, query, page, pageSize)
	case "gcr.io":
		// Google Container Registry 搜索
		return searchGCR(ctx, query, page, pageSize)
	case "quay.io":
		// Quay.io 搜索
		return searchQuay(ctx, query, page, pageSize)
	case "registry.k8s.io":
		// Kubernetes Registry 搜索
		return searchK8sRegistry(ctx, query, page, pageSize)
	default:
		// 默认使用 Docker Hub 搜索
		return searchDockerHub(ctx, query, page, pageSize)
	}
}

// searchGHCR 搜索 GitHub Container Registry
func searchGHCR(ctx context.Context, query string, page, pageSize int) (*SearchResult, error) {
	return searchGenericRegistry(ctx, "ghcr.io", "https://ghcr.io/v2/search", query, page, pageSize)
}

// searchGCR 搜索 Google Container Registry
func searchGCR(ctx context.Context, query string, page, pageSize int) (*SearchResult, error) {
	// GCR 不提供通用搜索API，但我们可以尝试查询特定仓库
	// 对于GCR，我们返回一个占位符结果，因为GCR本身没有公开的搜索API
	return &SearchResult{
		Count:    0,
		Next:     "",
		Previous: "",
		Results:  []Repository{},
	}, nil
}

// searchQuay 搜索 Quay.io
func searchQuay(ctx context.Context, query string, page, pageSize int) (*SearchResult, error) {
	return searchGenericRegistry(ctx, "quay.io", "https://quay.io/api/v1/find", query, page, pageSize)
}

// searchK8sRegistry 搜索 Kubernetes Registry
func searchK8sRegistry(ctx context.Context, query string, page, pageSize int) (*SearchResult, error) {
	// registry.k8s.io 也没有公开的搜索API，但我们可以通过其API获取一些仓库信息
	// 尝试获取特定路径下的仓库信息
	baseURL := "https://registry.k8s.io/v2/_catalog"
	params := url.Values{}
	params.Set("n", fmt.Sprintf("%d", pageSize))
	params.Set("last", query) // 这只是示例，实际可能需要其他方式

	fullURL := baseURL + "?" + params.Encode()
	
	req, err := http.NewRequestWithContext(ctx, "GET", fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建K8s registry请求失败: %v", err)
	}

	resp, err := utils.GetSearchHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求K8s registry API失败: %v", err)
	}
	defer safeCloseResponseBody(resp.Body, "k8s registry响应体")

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取K8s registry响应失败: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		// 如果无法访问，返回空结果而不是错误
		return &SearchResult{
			Count:    0,
			Next:     "",
			Previous: "",
			Results:  []Repository{},
		}, nil
	}

	// 解析响应
	var catalog struct {
		Repositories []string `json:"repositories"`
	}
	
	if err := json.Unmarshal(body, &catalog); err != nil {
		// 如果无法解析，也返回空结果
		return &SearchResult{
			Count:    0,
			Next:     "",
			Previous: "",
			Results:  []Repository{},
		}, nil
	}

	// 过滤匹配的仓库
	filteredRepos := []string{}
	for _, repo := range catalog.Repositories {
		if strings.Contains(strings.ToLower(repo), strings.ToLower(query)) {
			filteredRepos = append(filteredRepos, repo)
		}
	}

	// 转换为Repository格式
	repositories := make([]Repository, 0, len(filteredRepos))
	for _, repoName := range filteredRepos {
		repo := Repository{
			Name:          repoName,
			Description:   fmt.Sprintf("Kubernetes official image: %s", repoName),
			IsOfficial:    true,
			IsAutomated:   true,
			StarCount:     0,
			PullCount:     0,
			RepoOwner:     "kubernetes",
			LastUpdated:   "",
			Status:        1,
			Organization:  "kubernetes",
			PullsLastWeek: 0,
			Namespace:     "k8s",
			Source:        "registry.k8s.io",
		}
		repositories = append(repositories, repo)
	}

	return &SearchResult{
		Count:    len(repositories),
		Next:     "",
		Previous: "",
		Results:  repositories,
	}, nil
}

// searchGenericRegistry 通用注册表搜索函数
func searchGenericRegistry(ctx context.Context, source, endpoint, query string, page, pageSize int) (*SearchResult, error) {
	// 生成缓存键
	cacheKey := fmt.Sprintf("search_multi:%s:%s:%d:%d", source, query, page, pageSize)

	if cached, ok := searchCache.Get(cacheKey); ok {
		return cached.(*SearchResult), nil
	}

	// 构建请求URL
	var fullURL string
	var params url.Values

	if strings.Contains(endpoint, "search") {
		// Docker-like API
		params = url.Values{
			"query":     {query},
			"page":      {fmt.Sprintf("%d", page)},
			"page_size": {fmt.Sprintf("%d", pageSize)},
		}
		fullURL = endpoint + "?" + params.Encode()
	} else if strings.Contains(endpoint, "find") {
		// Quay-like API
		params = url.Values{
			"term": {query},
		}
		fullURL = endpoint + "?" + params.Encode()
	} else {
		// 默认情况
		params = url.Values{
			"q":         {query},
			"page":      {fmt.Sprintf("%d", page)},
			"page_size": {fmt.Sprintf("%d", pageSize)},
		}
		fullURL = endpoint + "?" + params.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, "GET", fullURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建请求失败: %v", err)
	}

	req.Header.Set("Accept", "application/json")
	
	resp, err := utils.GetSearchHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求%s API失败: %v", source, err)
	}
	defer safeCloseResponseBody(resp.Body, fmt.Sprintf("%s响应体", source))

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取%s响应失败: %v", source, err)
	}

	if resp.StatusCode != http.StatusOK {
		switch resp.StatusCode {
		case http.StatusTooManyRequests:
			return nil, fmt.Errorf("请求%s过于频繁，请稍后重试", source)
		case http.StatusNotFound:
			return nil, fmt.Errorf("在%s中未找到相关镜像", source)
		case http.StatusBadGateway, http.StatusServiceUnavailable:
			return nil, fmt.Errorf("%s服务暂时不可用，请稍后重试", source)
		default:
			return nil, fmt.Errorf("请求%s失败: 状态码=%d, 响应=%s", source, resp.StatusCode, string(body))
		}
	}

	var result *SearchResult
	
	if source == "quay.io" {
		// Quay.io 特殊处理
		var quayResult struct {
			Apps []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				Owner       string `json:"owner"`
				IsPublic    bool   `json:"is_public"`
			} `json:"apps"`
			Repositories []struct {
				Name        string `json:"name"`
				Description string `json:"description"`
				IsPublic    bool   `json:"is_public"`
				Namespace   string `json:"namespace"`
			} `json:"repositories"`
		}
		
		if err := json.Unmarshal(body, &quayResult); err != nil {
			return nil, fmt.Errorf("解析%s响应失败: %v", source, err)
		}

		// 转换Quay.io结果为通用格式
		repos := make([]Repository, 0)
		for _, app := range quayResult.Apps {
			repo := Repository{
				Name:        app.Name,
				Description: app.Description,
				IsOfficial:  false,
				IsAutomated: false,
				StarCount:   0,
				PullCount:   0,
				RepoOwner:   app.Owner,
				LastUpdated: "",
				Status:      1,
				Organization: app.Owner,
				PullsLastWeek: 0,
				Namespace:   app.Owner,
				Source:      source,
			}
			repos = append(repos, repo)
		}
		
		for _, repoData := range quayResult.Repositories {
			repo := Repository{
				Name:        repoData.Name,
				Description: repoData.Description,
				IsOfficial:  false,
				IsAutomated: repoData.IsPublic,
				StarCount:   0,
				PullCount:   0,
				RepoOwner:   repoData.Namespace,
				LastUpdated: "",
				Status:      1,
				Organization: repoData.Namespace,
				PullsLastWeek: 0,
				Namespace:   repoData.Namespace,
				Source:      source,
			}
			repos = append(repos, repo)
		}

		result = &SearchResult{
			Count:   len(repos),
			Next:    "",
			Previous: "",
			Results: repos,
		}
	} else {
		// 标准Docker Registry API格式
		result = &SearchResult{}
		if err := json.Unmarshal(body, result); err != nil {
			return nil, fmt.Errorf("解析%s响应失败: %v", source, err)
		}

		// 添加源标识到每个仓库
		for i := range result.Results {
			result.Results[i].Source = source
		}
	}

	// 缓存结果
	searchCache.SetWithTTL(cacheKey, result, cacheTTL)
	
	return result, nil
}

func filterSearchResults(results []Repository, query string) []Repository {
	searchTerm := strings.ToLower(strings.TrimPrefix(query, "library/"))
	filtered := make([]Repository, 0)

	for _, repo := range results {
		repoName := strings.ToLower(repo.Name)
		repoDesc := strings.ToLower(repo.Description)

		score := 0

		if repoName == searchTerm {
			score += 100
		}

		if strings.HasPrefix(repoName, searchTerm) {
			score += 50
		}

		if strings.Contains(repoName, searchTerm) {
			score += 30
		}

		if strings.Contains(repoDesc, searchTerm) {
			score += 10
		}

		if repo.IsOfficial {
			score += 20
		}

		if score > 0 {
			filtered = append(filtered, repo)
		}
	}

	sort.Slice(filtered, func(i, j int) bool {
		if filtered[i].IsOfficial != filtered[j].IsOfficial {
			return filtered[i].IsOfficial
		}
		return filtered[i].PullCount > filtered[j].PullCount
	})

	return filtered
}

// normalizeRepository 统一规范化仓库信息
func normalizeRepository(repo *Repository) {
	if repo.IsOfficial {
		repo.Namespace = "library"
		if !strings.Contains(repo.Name, "/") {
			repo.Name = "library/" + repo.Name
		}
	} else {
		if repo.Namespace == "" && repo.RepoOwner != "" {
			repo.Namespace = repo.RepoOwner
		}

		if strings.Contains(repo.Name, "/") {
			parts := strings.Split(repo.Name, "/")
			if len(parts) > 1 {
				if repo.Namespace == "" {
					repo.Namespace = parts[0]
				}
				repo.Name = parts[len(parts)-1]
			}
		}
	}
}

// mergeSearchResults 合并来自不同源的搜索结果
func mergeSearchResults(results []MultiSourceSearchResult) *SearchResult {
	allRepos := make([]Repository, 0)
	totalCount := 0

	for _, result := range results {
		if result.Error == "" {
			allRepos = append(allRepos, result.Results...)
			totalCount += result.Count
		}
	}

	return &SearchResult{
		Count:   totalCount,
		Next:    "", // 多源合并后不再适用分页
		Previous: "",
		Results: allRepos,
	}
}

// searchDockerHub 搜索镜像
func searchDockerHub(ctx context.Context, query string, page, pageSize int) (*SearchResult, error) {
	return searchDockerHubWithDepth(ctx, query, page, pageSize, 0)
}

func searchDockerHubWithDepth(ctx context.Context, query string, page, pageSize int, depth int) (*SearchResult, error) {
	if depth > 1 {
		return nil, fmt.Errorf("搜索请求过于复杂，请尝试更具体的关键词")
	}
	cacheKey := fmt.Sprintf("search:%s:%d:%d", query, page, pageSize)

	if cached, ok := searchCache.Get(cacheKey); ok {
		return cached.(*SearchResult), nil
	}

	isUserRepo := strings.Contains(query, "/")
	var namespace, repoName string

	if isUserRepo {
		parts := strings.Split(query, "/")
		if len(parts) == 2 {
			namespace = parts[0]
			repoName = parts[1]
		}
	}

	baseURL := "https://registry.hub.docker.com/v2"
	var fullURL string
	var params url.Values

	if isUserRepo && namespace != "" {
		fullURL = fmt.Sprintf("%s/repositories/%s/", baseURL, namespace)
		params = url.Values{
			"page":      {fmt.Sprintf("%d", page)},
			"page_size": {fmt.Sprintf("%d", pageSize)},
		}
	} else {
		fullURL = baseURL + "/search/repositories/"
		params = url.Values{
			"query":     {query},
			"page":      {fmt.Sprintf("%d", page)},
			"page_size": {fmt.Sprintf("%d", pageSize)},
		}
	}

	fullURL = fullURL + "?" + params.Encode()

	resp, err := utils.GetSearchHTTPClient().Get(fullURL)
	if err != nil {
		return nil, fmt.Errorf("请求Docker Hub API失败: %v", err)
	}
	defer safeCloseResponseBody(resp.Body, "搜索响应体")

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取响应失败: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		switch resp.StatusCode {
		case http.StatusTooManyRequests:
			return nil, fmt.Errorf("请求过于频繁，请稍后重试")
		case http.StatusNotFound:
			if isUserRepo && namespace != "" {
				return searchDockerHubWithDepth(ctx, repoName, page, pageSize, depth+1)
			}
			return nil, fmt.Errorf("未找到相关镜像")
		case http.StatusBadGateway, http.StatusServiceUnavailable:
			return nil, fmt.Errorf("Docker Hub服务暂时不可用，请稍后重试")
		default:
			return nil, fmt.Errorf("请求失败: 状态码=%d, 响应=%s", resp.StatusCode, string(body))
		}
	}

	var result *SearchResult
	if isUserRepo && namespace != "" {
		var userRepos struct {
			Count    int          `json:"count"`
			Next     string       `json:"next"`
			Previous string       `json:"previous"`
			Results  []Repository `json:"results"`
		}
		if err := json.Unmarshal(body, &userRepos); err != nil {
			return nil, fmt.Errorf("解析响应失败: %v", err)
		}

		result = &SearchResult{
			Count:    userRepos.Count,
			Next:     userRepos.Next,
			Previous: userRepos.Previous,
			Results:  make([]Repository, 0),
		}

		for _, repo := range userRepos.Results {
			if repoName == "" || strings.Contains(strings.ToLower(repo.Name), strings.ToLower(repoName)) {
				repo.Namespace = namespace
				normalizeRepository(&repo)
				result.Results = append(result.Results, repo)
			}
		}

		if len(result.Results) == 0 {
			return searchDockerHubWithDepth(ctx, repoName, page, pageSize, depth+1)
		}

		result.Count = len(result.Results)
	} else {
		result = &SearchResult{}
		if err := json.Unmarshal(body, &result); err != nil {
			return nil, fmt.Errorf("解析响应失败: %v", err)
		}

		for i := range result.Results {
			normalizeRepository(&result.Results[i])
		}

		if isUserRepo && namespace != "" {
			filteredResults := make([]Repository, 0)
			for _, repo := range result.Results {
				if strings.EqualFold(repo.Namespace, namespace) {
					filteredResults = append(filteredResults, repo)
				}
			}
			result.Results = filteredResults
			result.Count = len(filteredResults)
		}
	}

	searchCache.Set(cacheKey, result)
	return result, nil
}

func isRetryableError(err error) bool {
	if err == nil {
		return false
	}

	if strings.Contains(err.Error(), "timeout") ||
		strings.Contains(err.Error(), "connection refused") ||
		strings.Contains(err.Error(), "no such host") ||
		strings.Contains(err.Error(), "too many requests") {
		return true
	}

	return false
}

// getRepositoryTags 获取仓库标签信息
func getRepositoryTags(ctx context.Context, namespace, name string, page, pageSize int) ([]TagInfo, bool, error) {
	if namespace == "" || name == "" {
		return nil, false, fmt.Errorf("无效输入：命名空间和名称不能为空")
	}

	if page <= 0 {
		page = 1
	}
	if pageSize <= 0 || pageSize > 100 {
		pageSize = 100
	}

	cacheKey := fmt.Sprintf("tags:%s:%s:page_%d", namespace, name, page)
	if cached, ok := searchCache.Get(cacheKey); ok {
		result := cached.(TagPageResult)
		return result.Tags, result.HasMore, nil
	}

	baseURL := fmt.Sprintf("https://registry.hub.docker.com/v2/repositories/%s/%s/tags", namespace, name)
	params := url.Values{}
	params.Set("page", fmt.Sprintf("%d", page))
	params.Set("page_size", fmt.Sprintf("%d", pageSize))
	params.Set("ordering", "last_updated")

	fullURL := baseURL + "?" + params.Encode()

	pageResult, err := fetchTagPage(ctx, fullURL, 3)
	if err != nil {
		return nil, false, fmt.Errorf("获取标签失败: %v", err)
	}

	hasMore := pageResult.Next != ""

	result := TagPageResult{Tags: pageResult.Results, HasMore: hasMore}
	searchCache.SetWithTTL(cacheKey, result, 30*time.Minute)

	return pageResult.Results, hasMore, nil
}

func fetchTagPage(ctx context.Context, url string, maxRetries int) (*struct {
	Count    int       `json:"count"`
	Next     string    `json:"next"`
	Previous string    `json:"previous"`
	Results  []TagInfo `json:"results"`
}, error) {
	var lastErr error

	for retry := 0; retry < maxRetries; retry++ {
		if retry > 0 {
			time.Sleep(time.Duration(retry) * 500 * time.Millisecond)
		}

		resp, err := utils.GetSearchHTTPClient().Get(url)
		if err != nil {
			lastErr = err
			if isRetryableError(err) && retry < maxRetries-1 {
				continue
			}
			return nil, fmt.Errorf("发送请求失败: %v", err)
		}

		body, err := func() ([]byte, error) {
			defer safeCloseResponseBody(resp.Body, "标签响应体")
			return io.ReadAll(resp.Body)
		}()

		if err != nil {
			lastErr = err
			if retry < maxRetries-1 {
				continue
			}
			return nil, fmt.Errorf("读取响应失败: %v", err)
		}

		if resp.StatusCode != http.StatusOK {
			lastErr = fmt.Errorf("状态码=%d, 响应=%s", resp.StatusCode, string(body))
			if resp.StatusCode >= 400 && resp.StatusCode < 500 && resp.StatusCode != 429 {
				return nil, fmt.Errorf("请求失败: %v", lastErr)
			}
			if retry < maxRetries-1 {
				continue
			}
			return nil, fmt.Errorf("请求失败: %v", lastErr)
		}

		var result struct {
			Count    int       `json:"count"`
			Next     string    `json:"next"`
			Previous string    `json:"previous"`
			Results  []TagInfo `json:"results"`
		}
		if err := json.Unmarshal(body, &result); err != nil {
			lastErr = err
			if retry < maxRetries-1 {
				continue
			}
			return nil, fmt.Errorf("解析响应失败: %v", err)
		}

		return &result, nil
	}

	return nil, lastErr
}

func parsePaginationParams(c *gin.Context, defaultPageSize int) (page, pageSize int) {
	page = 1
	pageSize = defaultPageSize

	if p := c.Query("page"); p != "" {
		fmt.Sscanf(p, "%d", &page)
	}
	if ps := c.Query("page_size"); ps != "" {
		fmt.Sscanf(ps, "%d", &pageSize)
	}

	return page, pageSize
}

func safeCloseResponseBody(body io.ReadCloser, context string) {
	if body != nil {
		if err := body.Close(); err != nil {
			fmt.Printf("关闭%s失败: %v\n", context, err)
		}
	}
}

func sendErrorResponse(c *gin.Context, message string) {
	c.JSON(http.StatusBadRequest, gin.H{"error": message})
}

// searchMultiRegistries 并行搜索多个注册表
func searchMultiRegistries(ctx context.Context, query string, page, pageSize int) ([]MultiSourceSearchResult, error) {
	enabledRegistries := getEnabledRegistries()
	
	var results []MultiSourceSearchResult
	
	// 并发搜索所有启用的注册表
	var wg sync.WaitGroup
	resultChan := make(chan MultiSourceSearchResult, len(enabledRegistries))
	
	for domain := range enabledRegistries {
		wg.Add(1)
		go func(domain string) {
			defer wg.Done()
			
			result, err := searchRegistryByDomain(ctx, domain, query, page, pageSize)
			if err != nil {
				resultChan <- MultiSourceSearchResult{
					Source:  domain,
					Error:   err.Error(),
					Count:   0,
					Results: []Repository{},
				}
			} else {
				resultChan <- MultiSourceSearchResult{
					Source:  domain,
					Count:   result.Count,
					Next:    result.Next,
					Previous: result.Previous,
					Results: result.Results,
				}
			}
		}(domain)
	}
	
	// 等待所有goroutine完成
	go func() {
		wg.Wait()
		close(resultChan)
	}()
	
	// 收集结果
	for res := range resultChan {
		results = append(results, res)
	}
	
	return results, nil
}

// searchMultiRegistriesMerged 并行搜索多个注册表并合并结果
func searchMultiRegistriesMerged(ctx context.Context, query string, page, pageSize int) (*SearchResult, error) {
	multiResults, err := searchMultiRegistries(ctx, query, page, pageSize)
	if err != nil {
		return nil, err
	}
	
	return mergeSearchResults(multiResults), nil
}

// RegisterSearchRoute 注册搜索相关路由
func RegisterSearchRoute(r *gin.Engine) {
	r.GET("/search", func(c *gin.Context) {
		query := c.Query("q")
		if query == "" {
			sendErrorResponse(c, "搜索关键词不能为空")
			return
		}

		// 检查是否需要多源搜索
		multiSource := c.Query("multi_source") == "true" || c.Query("all_sources") == "true"
		mergedResult := c.Query("merged") == "true"
		
		page, pageSize := parsePaginationParams(c, defaultPageSize)

		if multiSource {
			// 多源搜索
			if mergedResult {
				// 返回合并后的结果
				result, err := searchMultiRegistriesMerged(c.Request.Context(), query, page, pageSize)
				if err != nil {
					sendErrorResponse(c, err.Error())
					return
				}
				
				c.JSON(http.StatusOK, result)
			} else {
				// 返回各源的独立结果
				results, err := searchMultiRegistries(c.Request.Context(), query, page, pageSize)
				if err != nil {
					sendErrorResponse(c, err.Error())
					return
				}
				
				c.JSON(http.StatusOK, gin.H{
					"multi_source_results": results,
					"total_sources":        len(results),
					"query":                query,
					"page":                 page,
					"page_size":            pageSize,
				})
			}
		} else {
			// 单源搜索，默认使用Docker Hub
			result, err := searchDockerHub(c.Request.Context(), query, page, pageSize)
			if err != nil {
				sendErrorResponse(c, err.Error())
				return
			}

			c.JSON(http.StatusOK, result)
		}
	})

	// 新增多源搜索专用路由
	r.GET("/search/multi", func(c *gin.Context) {
		query := c.Query("q")
		if query == "" {
			sendErrorResponse(c, "搜索关键词不能为空")
			return
		}

		page, pageSize := parsePaginationParams(c, defaultPageSize)

		results, err := searchMultiRegistries(c.Request.Context(), query, page, pageSize)
		if err != nil {
			sendErrorResponse(c, err.Error())
			return
		}

		c.JSON(http.StatusOK, gin.H{
			"multi_source_results": results,
			"total_sources":        len(results),
			"query":                query,
			"page":                 page,
			"page_size":            pageSize,
		})
	})

	r.GET("/tags/:namespace/:name", func(c *gin.Context) {
		namespace := c.Param("namespace")
		name := c.Param("name")

		if namespace == "" || name == "" {
			sendErrorResponse(c, "命名空间和名称不能为空")
			return
		}

		page, pageSize := parsePaginationParams(c, 100)

		tags, hasMore, err := getRepositoryTags(c.Request.Context(), namespace, name, page, pageSize)
		if err != nil {
			sendErrorResponse(c, err.Error())
			return
		}

		if c.Query("page") != "" || c.Query("page_size") != "" {
			c.JSON(http.StatusOK, gin.H{
				"tags":      tags,
				"has_more":  hasMore,
				"page":      page,
				"page_size": pageSize,
			})
		} else {
			c.JSON(http.StatusOK, tags)
		}
	})
}
