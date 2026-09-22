# macOS 凭据存储与验证边界

Darwin 桌面使用 Security.framework 的 SecItem API，将凭据存入用户默认的 file-based Keychain。查询限定同一个 keychain、固定 service `workbench.prayu.desktop.credentials.v1` 与调用方原有 credential name。API Key 不进入命令行、子进程、环境变量、SQLite 或公开状态；可写临时字节副本在使用后清理。现有 Go string 接口不保证内存里没有秘密副本。

实现要求 CGO，与现有 Wails/SQLite 桌面要求一致；Darwin CGO=0、Linux 等未支持平台保持 `unsupported`，不提供明文回退。`store_available=true` 表示编译时具备 Keychain 能力，锁定、用户拒绝或取消均作为操作错误；只有真实缺项才投影为未配置。更新沿用现有 Provider 凭据 revision、资格撤销和 Registry reload，不把 SQLite 与 Keychain 描述成跨系统事务。

系统可能显示解锁或授权提示。同步 Security 调用在结束前不会因 Go context 超时而留在后台继续写入；上下文取消会阻止尚未开始的下一次调用。读取状态也核验秘密有效性，因此可能触发系统授权。实现不切换进程全局交互策略、不自动重试用户取消。

当前 ad-hoc 预览选用 file-based 兼容方案；Apple 通常建议 data-protection Keychain，但其访问权限还依赖应用签名的 entitlements/provisioning。此实现不改变现有预览、notarization 或发行就绪状态。[Apple TN3137](https://developer.apple.com/documentation/technotes/tn3137-on-mac-keychains)

## 自动验证

普通 `go test ./internal/credential` 在 Darwin 测试输入拒绝、OSStatus 错误映射、更新/创建冲突和取消时序，不触碰真实 keychain。Windows 测试不会执行 Darwin 文件，Darwin CGO=0 交叉编译仅证明 fallback 可构建。

在可销毁的 Mac runner 上显式运行真实系统集成：

```sh
TRAVERSE_TEST_KEYCHAIN=1 CGO_ENABLED=1 go test -tags=keychainintegration -count=1 -timeout=90s ./internal/credential
```

同时需要 tag 和开关；没有开关时集成测试明确 skip。夹具在临时目录创建私有 keychain，密码和假凭据仅在内存生成，绝不命名为 `login.keychain` 或 `System.keychain`。所有 item 操作指定该 target；测试前后只读比较默认 keychain/search list，不调用修改它们或全局交互设置的 API。清理只删除已创建的私有 keychain。检查真实保存、替换、新 store 重读、幂等删除、跨 account/service/keychain 隔离，以及损坏项拒绝。没有真实模型请求。

## 仍需实际应用验收

隔离 CRUD 不能证明实际 `.app` 的登录会话、ACL 和授权提示体验。使用独立 Mac 测试用户/可销毁环境，验证首次配置、替换、删除、退出重开、锁定后恢复、手动取消、同版本重启和不同 ad-hoc 构建升级。锁定/取消的真实系统对话框不在上述无人值守测试中伪造通过。记录准确的应用提交、OS、架构和二进制身份；Mac 15 runner 通过不能证明 macOS 11 或正式发行已就绪。
