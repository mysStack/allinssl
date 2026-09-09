# ALLinSSL 部署说明

本目录提供单实例部署清单。ALLinSSL 将 SQLite 数据库、证书和运行配置写入本地目录，因此 Docker 和 Kubernetes 均只能运行一个副本；升级时必须等待旧实例停止。

镜像地址：`ghcr.io/mysstack/allinssl:custom`。生产环境建议使用已验证提交对应的 `sha-<commit>` 标签，而不是会滚动更新的 `custom` 标签。

## Docker Compose

```bash
cd deploy
cp .env.example .env
chmod 600 .env
# 编辑 .env：至少修改 ALLINSSL_PWD 和 ALLINSSL_URL
docker compose pull
docker compose up -d
docker compose logs -f allinssl
```

浏览器访问 `http://<服务器地址>:<ALLINSSL_PORT><ALLINSSL_URL>`。例如 `ALLINSSL_PORT=8888`、`ALLINSSL_URL=/panel-random` 时访问 `http://server:8888/panel-random`；安全入口会写入浏览器会话后跳转到登录页，不要手工拼接 `/login`。

数据、日志和插件分别保存到 Compose 命名卷。备份前先停止容器，或对宿主机的 Docker volume 做快照。升级镜像后执行：

```bash
docker compose pull
docker compose up -d
```

## Kubernetes

Kubernetes 清单使用默认 `StorageClass` 创建一个 `5Gi`、`ReadWriteOnce` PVC，并以 `Recreate` 策略维持单副本。若集群没有默认存储类，请在 `kubernetes/pvc.yaml` 的 `spec` 下添加正确的 `storageClassName`。

```bash
cd deploy/kubernetes
cp secret.env.example secret.env
chmod 600 secret.env
# 编辑 secret.env：必须修改 ALLINSSL_PWD 和 ALLINSSL_URL
kubectl apply -k .
kubectl -n allinssl get pods,svc,pvc
kubectl -n allinssl logs -f deployment/allinssl
```

默认 Service 是仅集群内访问的 `ClusterIP`。临时本地访问：

```bash
kubectl -n allinssl port-forward service/allinssl 8888:80
```

随后访问 `http://127.0.0.1:8888<ALLINSSL_URL>`。要经域名公开访问，复制 `ingress.example.yaml` 为集群使用的 Ingress 文件，修改 `ingressClassName` 和 `host` 后应用；TLS 由 Ingress 控制器终止，应用容器保持 HTTP。

## 初始化与安全

`ALLINSSL_USER`、`ALLINSSL_PWD` 和 `ALLINSSL_URL` 只会在空数据目录第一次启动时初始化。数据卷已经存在时，修改环境变量不会重置登录信息；请从 ALLINSSL 界面或其管理命令修改。不要将 `.env`、`kubernetes/secret.env`、数据卷或证书提交到 Git。

请将公开端口限制为受信网络，或使用带 TLS 与访问控制的反向代理/Ingress。应用运行时生成的证书、账号和 DNS 凭据都保存在持久化数据目录中，应纳入加密备份并严格控制读取权限。
