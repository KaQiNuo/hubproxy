要本地启动和调试这个应用，您可以按照以下步骤操作：

## 本地启动和调试方法

### 方法1：使用Go直接运行（推荐用于开发调试）

```bash
# 1. 确保在项目根目录
cd e:\temp\code\hubproxy\src

# 2. 下载依赖（如果前面的网络问题已解决）
go mod tidy

# 3. 运行应用
go run main.go
```

如果网络连接有问题，可以配置Go代理：

```bash
# 设置Go代理（如果需要）
go env -w GOPROXY=https://goproxy.cn,direct

# 然后重新下载依赖
go mod tidy
go run main.go
```

### 方法2：构建后运行

```bash
# 1. 构建二进制文件
go build -o hubproxy.exe main.go

# 2. 运行
./hubproxy.exe
```

### 方法3：使用Docker运行

```bash
# 1. 构建Docker镜像
docker build -t hubproxy .

# 2. 运行容器
docker run -p 5000:5000 hubproxy
```

### 方法4：使用docker-compose

```bash
# 运行整个服务栈
docker-compose up
```

## 调试配置

### 1. 配置文件
- 确保`src/config.toml`文件存在且配置正确
- 默认情况下，服务会在`0.0.0.0:5000`上监听

### 2. 开发调试技巧

如果要进行详细的调试，可以在VS Code中创建以下launch配置：

创建 `.vscode/launch.json` 文件：

```json
{
    "version": "0.2.0",
    "configurations": [
        {
            "name": "Launch hubproxy",
            "type": "go",
            "request": "launch",
            "mode": "debug",
            "program": "${workspaceFolder}/src",
            "env": {},
            "args": []
        }
    ]
}
```

### 3. 启动服务后的测试

启动服务后，您可以使用以下URL进行测试：

- 访问首页：`http://localhost:5000`
- 搜索镜像：`http://localhost:5000/search?q=nginx`
- 多源搜索：`http://localhost:5000/search?q=redis&multi_source=true`
- 合并结果搜索：`http://localhost:5000/search?q=redis&multi_source=true&merged=true`
- 专用多源搜索：`http://localhost:5000/search/multi?q=redis`
- 获取标签：`http://localhost:5000/tags/library/nginx`

### 4. 日志查看

应用启动后会显示日志信息，包括：
- 服务器启动信息
- 请求日志
- 错误信息（如果有）

### 注意事项

1. **端口占用**：如果5000端口被占用，可以在`config.toml`中修改端口号
2. **防火墙**：确保防火墙允许5000端口的访问
3. **依赖下载**：首次运行可能需要较长时间下载依赖，特别是网络不佳的情况下

如果您遇到任何网络问题导致依赖下载失败，可以尝试更换Go模块代理或使用国内镜像源。