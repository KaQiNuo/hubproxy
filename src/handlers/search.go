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

	"hubproxy/config"
	"hubproxy/utils"

	"github.com/gin-gonic/gin"
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
	Source   string       `json:"source"`
	Count    int          `json:"count"`
	Next     string       `json:"next"`
	Previous string       `json:"previous"`
	Results  []Repository `json:"results"`
	Error    string       `json:"error,omitempty"`
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
	// GHCR没有公共搜索API，我们需要使用不同的方法来查找包
	// 尝试通过GitHub API查找相关的包
	return searchGHCRByPackagesAPI(ctx, query, page, pageSize)
}

// searchGHCRByPackagesAPI 通过GitHub Packages API搜索GHCR包
func searchGHCRByPackagesAPI(ctx context.Context, query string, page, pageSize int) (*SearchResult, error) {
	// 直接检查是否存在指定的包
	// 尝试直接构建包的路径并检查是否存在
	repositories := make([]Repository, 0)

	// 检查是否是完整的包路径（如 BerriAI/litellm）
	if strings.Contains(query, "/") {
		parts := strings.Split(query, "/")
		if len(parts) == 2 {
			owner := parts[0]
			packageName := parts[1]
			// 直接检查这个包是否存在
			if checkDirectPackageExists(ctx, owner, packageName) {
				// 创建Repository对象
				repo := Repository{
					Name:          fmt.Sprintf("%s/%s", owner, packageName),
					Description:   "",
					IsOfficial:    false,
					IsAutomated:   true,
					StarCount:     0,
					PullCount:     0,
					RepoOwner:     owner,
					LastUpdated:   "",
					Status:        1,
					Organization:  owner,
					PullsLastWeek: 0,
					Namespace:     owner,
					Source:        "ghcr.io",
				}
				repositories = append(repositories, repo)
				return &SearchResult{
					Count:    1,
					Next:     "",
					Previous: "",
					Results:  repositories,
				}, nil
			}
		}
	}

	// 如果不是完整路径，尝试搜索仓库
	apiURL := fmt.Sprintf("https://api.github.com/search/repositories?q=%s+in:name,description,readme", url.QueryEscape(query))

	req, err := http.NewRequestWithContext(ctx, "GET", apiURL, nil)
	if err != nil {
		return nil, fmt.Errorf("创建GitHub API请求失败: %v", err)
	}

	// 设置适当的请求头
	req.Header.Set("Accept", "application/vnd.github.v3+json")
	req.Header.Set("User-Agent", "HubProxy")

	resp, err := utils.GetSearchHTTPClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("请求GitHub API失败: %v", err)
	}
	defer safeCloseResponseBody(resp.Body, "GitHub API响应体")

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("读取GitHub API响应失败: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		// 如果GitHub API不可用，返回空结果而不是错误
		return &SearchResult{
			Count:    0,
			Next:     "",
			Previous: "",
			Results:  []Repository{},
		}, nil
	}

	// 解析GitHub仓库搜索结果
	var githubResult struct {
		TotalCount int `json:"total_count"`
		Items      []struct {
			ID       int    `json:"id"`
			Name     string `json:"name"`
			FullName string `json:"full_name"`
			Owner    struct {
				Login string `json:"login"`
			} `json:"owner"`
			Description string `json:"description"`
			Stars       int    `json:"stargazers_count"`
			UpdatedAt   string `json:"updated_at"`
		} `json:"items"`
	}

	if err := json.Unmarshal(body, &githubResult); err != nil {
		return nil, fmt.Errorf("解析GitHub API响应失败: %v", err)
	}

	// 现在对每个仓库尝试查找对应的容器包
	// 计算分页范围
	startIdx := (page - 1) * pageSize
	if startIdx >= len(githubResult.Items) {
		return &SearchResult{
			Count:    githubResult.TotalCount,
			Next:     "",
			Previous: "",
			Results:  []Repository{},
		}, nil
	}

	endIdx := startIdx + pageSize
	if endIdx > len(githubResult.Items) {
		endIdx = len(githubResult.Items)
	}

	selectedItems := githubResult.Items[startIdx:endIdx]

	for _, item := range selectedItems {
		// 直接检查这个包是否存在
		if checkDirectPackageExists(ctx, item.Owner.Login, item.Name) {
			// 创建Repository对象
			repo := Repository{
				Name:          fmt.Sprintf("%s/%s", item.Owner.Login, item.Name),
				Description:   item.Description,
				IsOfficial:    false,
				IsAutomated:   true,
				StarCount:     item.Stars,
				PullCount:     0,
				RepoOwner:     item.Owner.Login,
				LastUpdated:   item.UpdatedAt,
				Status:        1,
				Organization:  item.Owner.Login,
				PullsLastWeek: 0,
				Namespace:     item.Owner.Login,
				Source:        "ghcr.io",
			}
			repositories = append(repositories, repo)
		}
	}

	return &SearchResult{
		Count:    len(repositories),
		Next:     "",
		Previous: "",
		Results:  repositories,
	}, nil
}

// checkDirectPackageExists 直接检查包是否存在
func checkDirectPackageExists(ctx context.Context, owner, packageName string) bool {
	// 直接构建包的URL并检查是否存在
	packageURL := fmt.Sprintf("https://ghcr.io/v2/%s/%s/tags/list", owner, packageName)

	req, err := http.NewRequestWithContext(ctx, "GET", packageURL, nil)
	if err != nil {
		return false
	}

	// 设置适当的请求头
	req.Header.Set("User-Agent", "HubProxy")

	resp, err := utils.GetSearchHTTPClient().Do(req)
	if err != nil {
		return false
	}
	defer safeCloseResponseBody(resp.Body, "GHCR API响应体")

	// 检查响应状态码
	// 如果返回200或401（需要认证但存在），则认为包存在
	return resp.StatusCode == http.StatusOK || resp.StatusCode == http.StatusUnauthorized
}

// checkRepositoryForContainerPackage 检查仓库是否有容器包
func checkRepositoryForContainerPackage(ctx context.Context, owner, repo string) (bool, string) {
	fmt.Printf("检查仓库 %s/%s 是否有容器包\n", owner, repo)

	// 尝试用户和组织的接口
	endpoints := []string{
		fmt.Sprintf("https://api.github.com/users/%s/packages?package_type=container", owner),
		fmt.Sprintf("https://api.github.com/orgs/%s/packages?package_type=container", owner),
	}

	for _, packageURL := range endpoints {
		fmt.Printf("尝试访问: %s\n", packageURL)
		req, err := http.NewRequestWithContext(ctx, "GET", packageURL, nil)
		if err != nil {
			fmt.Printf("创建请求失败: %v\n", err)
			continue
		}

		req.Header.Set("Accept", "application/vnd.github.v3+json")
		req.Header.Set("User-Agent", "HubProxy")

		resp, err := utils.GetSearchHTTPClient().Do(req)
		if err != nil {
			fmt.Printf("请求失败: %v\n", err)
			continue
		}
		defer safeCloseResponseBody(resp.Body, "GitHub Packages API响应体")

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			fmt.Printf("读取响应失败: %v\n", err)
			continue
		}

		if resp.StatusCode != http.StatusOK {
			fmt.Printf("响应状态码: %d, 响应内容: %s\n", resp.StatusCode, string(body))
			continue
		}

		fmt.Printf("成功获取包列表，响应长度: %d\n", len(body))

		// 解析包列表
		var packages []struct {
			Name        string `json:"name"`
			PackageType string `json:"package_type"`
			Owner       struct {
				Login string `json:"login"`
			} `json:"owner"`
		}

		if err := json.Unmarshal(body, &packages); err != nil {
			fmt.Printf("解析包列表失败: %v, 响应内容: %s\n", err, string(body))
			continue
		}

		fmt.Printf("解析到 %d 个包\n", len(packages))
		for i, pkg := range packages {
			fmt.Printf("第%d个包: %s/%s (类型: %s)\n", i+1, pkg.Owner.Login, pkg.Name, pkg.PackageType)
		}

		// 查找匹配的包名
		for _, pkg := range packages {
			if strings.EqualFold(pkg.Name, repo) && strings.EqualFold(pkg.PackageType, "container") {
				fmt.Printf("找到匹配的容器包: %s/%s\n", pkg.Owner.Login, pkg.Name)
				return true, fmt.Sprintf("%s/%s", pkg.Owner.Login, pkg.Name)
			}
		}
	}

	fmt.Printf("未找到 %s/%s 的容器包\n", owner, repo)
	return false, ""
}

// searchGCR 搜索 Google Container Registry
func searchGCR(ctx context.Context, query string, page, pageSize int) (*SearchResult, error) {
	// GCR 不提供通用搜索API，但我们可以尝试查询特定仓库
	// 作为替代方案，我们可以尝试构建可能的仓库路径并检查它们是否存在
	// 这里我们返回空结果，但不返回错误，这样合并时不会失败
	return &SearchResult{
		Count:    0,
		Next:     "",
		Previous: "",
		Results:  []Repository{}, // 返回空数组而不是nil，避免前端错误
	}, nil
}

// searchQuay 搜索 Quay.io
func searchQuay(ctx context.Context, query string, page, pageSize int) (*SearchResult, error) {
	return searchGenericRegistry(ctx, "quay.io", "https://quay.io/api/v1/find", query, page, pageSize)
}

// searchK8sRegistry 搜索 Kubernetes Registry
func searchK8sRegistry(ctx context.Context, query string, page, pageSize int) (*SearchResult, error) {
	// registry.k8s.io 也没有公开的搜索API，所以我们尝试列出所有仓库并过滤
	// 由于没有直接的搜索API，我们获取仓库列表并进行客户端过滤
	baseURL := "https://registry.k8s.io/v2/_catalog"
	params := url.Values{}
	params.Set("n", fmt.Sprintf("%d", 10000)) // 获取尽可能多的仓库

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

	// 过滤匹配的仓库 - 匹配查询词
	filteredRepos := []string{}
	queryLower := strings.ToLower(query)
	for _, repo := range catalog.Repositories {
		if strings.Contains(strings.ToLower(repo), queryLower) {
			filteredRepos = append(filteredRepos, repo)
		}
	}

	// 分页处理
	startIdx := (page - 1) * pageSize
	if startIdx >= len(filteredRepos) {
		return &SearchResult{
			Count:    len(filteredRepos),
			Next:     "",
			Previous: "",
			Results:  []Repository{},
		}, nil
	}

	endIdx := startIdx + pageSize
	if endIdx > len(filteredRepos) {
		endIdx = len(filteredRepos)
	}

	selectedRepos := filteredRepos[startIdx:endIdx]

	// 转换为Repository格式
	repositories := make([]Repository, 0, len(selectedRepos))
	for _, repoName := range selectedRepos {
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
			Namespace:     "registry.k8s.io",
			Source:        "registry.k8s.io",
		}
		repositories = append(repositories, repo)
	}

	return &SearchResult{
		Count:    len(filteredRepos), // 总数是过滤后的总数
		Next:     "",                 // K8s registry不支持标准分页
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
			return nil, fmt.Errorf("请求%s失败: 状态码=%d", source, resp.StatusCode)
		}
	}

	// 检查响应内容类型，防止HTML响应
	contentType := resp.Header.Get("Content-Type")
	if strings.Contains(strings.ToLower(contentType), "text/html") {
		// 如果响应是HTML，尝试检查是否包含错误信息
		bodyStr := string(body)
		if strings.Contains(bodyStr, "<!DOCTYPE html") || strings.Contains(bodyStr, "<html") {
			return &SearchResult{
				Count:    0,
				Next:     "",
				Previous: "",
				Results:  []Repository{},
			}, nil
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
			// 如果解析失败，可能是HTML响应或其他格式，返回空结果
			return &SearchResult{
				Count:    0,
				Next:     "",
				Previous: "",
				Results:  []Repository{},
			}, nil
		}

		// 转换Quay.io结果为通用格式
		repos := make([]Repository, 0)
		for _, app := range quayResult.Apps {
			repo := Repository{
				Name:          app.Name,
				Description:   app.Description,
				IsOfficial:    false,
				IsAutomated:   false,
				StarCount:     0,
				PullCount:     0,
				RepoOwner:     app.Owner,
				LastUpdated:   "",
				Status:        1,
				Organization:  app.Owner,
				PullsLastWeek: 0,
				Namespace:     app.Owner,
				Source:        source,
			}
			repos = append(repos, repo)
		}

		for _, repoData := range quayResult.Repositories {
			repo := Repository{
				Name:          repoData.Name,
				Description:   repoData.Description,
				IsOfficial:    false,
				IsAutomated:   repoData.IsPublic,
				StarCount:     0,
				PullCount:     0,
				RepoOwner:     repoData.Namespace,
				LastUpdated:   "",
				Status:        1,
				Organization:  repoData.Namespace,
				PullsLastWeek: 0,
				Namespace:     repoData.Namespace,
				Source:        source,
			}
			repos = append(repos, repo)
		}

		result = &SearchResult{
			Count:    len(repos),
			Next:     "",
			Previous: "",
			Results:  repos,
		}
	} else {
		// 标准Docker Registry API格式
		result = &SearchResult{}
		if err := json.Unmarshal(body, result); err != nil {
			// 如果JSON解析失败，返回空结果而不是错误，这样不会影响其他源的结果
			return &SearchResult{
				Count:    0,
				Next:     "",
				Previous: "",
				Results:  []Repository{},
			}, nil
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
		// 即使某个源有错误，我们也处理其他源的结果
		if result.Results != nil {
			// 为每个仓库添加源信息（如果还没有的话）
			for i := range result.Results {
				if result.Results[i].Source == "" {
					result.Results[i].Source = result.Source
				}
			}
			allRepos = append(allRepos, result.Results...)
			// 使用实际结果数量而不是可能不准确的计数
			totalCount += len(result.Results)
		}
	}

	// 对合并的结果进行排序，优先显示官方仓库和拉取数高的仓库
	sort.Slice(allRepos, func(i, j int) bool {
		// 首先按是否官方排序
		if allRepos[i].IsOfficial != allRepos[j].IsOfficial {
			return allRepos[i].IsOfficial
		}
		// 然后按拉取数排序
		return allRepos[i].PullCount > allRepos[j].PullCount
	})

	return &SearchResult{
		Count:    totalCount,
		Next:     "", // 多源合并后不再适用分页
		Previous: "",
		Results:  allRepos,
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
				// 确保结果中有源信息
				for i := range result.Results {
					if result.Results[i].Source == "" {
						result.Results[i].Source = domain
					}
				}
				resultChan <- MultiSourceSearchResult{
					Source:   domain,
					Count:    result.Count,
					Next:     result.Next,
					Previous: result.Previous,
					Results:  result.Results,
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

		// 检查是否指定了特定源
		source := c.Query("source")

		page, pageSize := parsePaginationParams(c, defaultPageSize)

		if source != "" {
			// 按指定源搜索
			enabledRegistries := getEnabledRegistries()
			// 特殊处理docker.io，因为它是默认的注册表，即使在配置中没有明确列出
			if _, exists := enabledRegistries[source]; !exists && source != "docker.io" {
				// 源不存在，返回错误
				sendErrorResponse(c, "指定的源不存在或未启用")
				return
			}

			// 搜索指定源
			result, err := searchRegistryByDomain(c.Request.Context(), source, query, page, pageSize)
			if err != nil {
				// 即使有错误，也返回空结果而不是错误响应
				c.JSON(http.StatusOK, &SearchResult{
					Count:    0,
					Next:     "",
					Previous: "",
					Results:  []Repository{},
				})
				return
			}

			// 确保返回的结果中包含正确的Source字段
			for i := range result.Results {
				result.Results[i].Source = source
			}

			c.JSON(http.StatusOK, result)
		} else if multiSource {
			// 多源搜索
			if mergedResult {
				// 返回合并后的结果
				result, err := searchMultiRegistriesMerged(c.Request.Context(), query, page, pageSize)
				if err != nil {
					// 即使有错误，也返回空结果而不是错误响应
					c.JSON(http.StatusOK, &SearchResult{
						Count:    0,
						Next:     "",
						Previous: "",
						Results:  []Repository{},
					})
					return
				}

				c.JSON(http.StatusOK, result)
			} else {
				// 返回各源的独立结果
				results, _ := searchMultiRegistries(c.Request.Context(), query, page, pageSize)
				// 忽略错误，因为searchMultiRegistries已经在内部处理了所有错误情况

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

		results, _ := searchMultiRegistries(c.Request.Context(), query, page, pageSize)
		// 忽略错误，因为searchMultiRegistries已经在内部处理了所有错误情况

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
