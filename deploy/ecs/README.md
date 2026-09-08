# ITH5 ECS 部署步骤

这份步骤只负责把 ITH5 跑到 AWS ECS 上，不使用 RDS。PostgreSQL 作为 Docker 容器运行。

## 推荐路线

为了省钱和避免 Fargate 数据盘问题，默认推荐：

```text
Route 53 + ACM
        ↓
ALB HTTPS
        ↓
ECS on EC2 单实例
        ├── ith5-server 容器
        └── postgres:17-alpine 容器
             └── EBS host path: /opt/ith5/postgres
```

也可以用 Fargate，但 PostgreSQL 必须挂 EFS，否则任务重建会丢数据。

## 0. 本地准备

需要安装并登录：

```sh
aws configure
docker --version
```

设置变量，后面命令直接复用：

```sh
export AWS_REGION=ap-northeast-1
export AWS_ACCOUNT_ID=$(aws sts get-caller-identity --query Account --output text)
export ECR_REPO=ith5
export IMAGE_TAG=v1.0.0
export DOMAIN=ith5.example.com
```

## 1. 创建 ECR 并推送镜像

```sh
aws ecr create-repository \
  --repository-name "$ECR_REPO" \
  --region "$AWS_REGION"

aws ecr get-login-password --region "$AWS_REGION" \
  | docker login --username AWS --password-stdin "$AWS_ACCOUNT_ID.dkr.ecr.$AWS_REGION.amazonaws.com"

docker build \
  --build-arg VERSION="$IMAGE_TAG" \
  -t "$ECR_REPO:$IMAGE_TAG" \
  .

docker tag "$ECR_REPO:$IMAGE_TAG" \
  "$AWS_ACCOUNT_ID.dkr.ecr.$AWS_REGION.amazonaws.com/$ECR_REPO:$IMAGE_TAG"

docker push "$AWS_ACCOUNT_ID.dkr.ecr.$AWS_REGION.amazonaws.com/$ECR_REPO:$IMAGE_TAG"
```

## 2. 创建密钥

生成生产 JWT secret 和数据库密码：

```sh
openssl rand -base64 48
```

分别写入 Secrets Manager：

```sh
export POSTGRES_PASSWORD='替换成随机数据库密码'

aws secretsmanager create-secret \
  --name ith5/jwt-secret \
  --secret-string "替换成随机JWT密钥" \
  --region "$AWS_REGION"

aws secretsmanager create-secret \
  --name ith5/postgres-password \
  --secret-string "$POSTGRES_PASSWORD" \
  --region "$AWS_REGION"

aws secretsmanager create-secret \
  --name ith5/database-url \
  --secret-string "postgres://ith5:${POSTGRES_PASSWORD}@postgres:5432/ith5?sslmode=disable" \
  --region "$AWS_REGION"
```

## 3. 创建 ECS 集群

创建普通 ECS 集群：

```sh
aws ecs create-cluster \
  --cluster-name ith5 \
  --region "$AWS_REGION"
```

然后在 AWS Console 里创建 Auto Scaling Group capacity provider：

- EC2 AMI：Amazon ECS-optimized Amazon Linux 2023
- 实例类型：`t3.small` 或 `t4g.small`
- Desired capacity：`1`
- EBS：建议 30GB 起
- User data 里写入：

```sh
#!/bin/bash
echo ECS_CLUSTER=ith5 >> /etc/ecs/ecs.config
mkdir -p /opt/ith5/postgres
```

安全组：

- EC2 入站不需要开放 `5432`
- 如果前面有 ALB，EC2 只允许 ALB 安全组访问容器端口
- SSH `22` 只允许你的 IP

## 4. 创建 ALB 和 HTTPS

创建：

- ACM 证书：`$DOMAIN`
- Application Load Balancer：公网
- Listener：`443`
- Target group：
  - Target type：`instance`
  - Protocol：HTTP
  - Port：由 ECS 动态端口映射
  - Health check path：`/healthz`

Route 53 里把 `$DOMAIN` 指到 ALB。

## 5. 注册任务定义

复制 `deploy/ecs/task-definition.ec2.json` 为临时文件，替换这些占位符：

- `AWS_ACCOUNT_ID`
- `AWS_REGION`
- `IMAGE_TAG`
- `YOUR_DOMAIN`
- `POSTGRES_PASSWORD_SECRET_ARN`
- `DATABASE_URL_SECRET_ARN`
- `JWT_SECRET_ARN`

然后注册：

```sh
aws ecs register-task-definition \
  --cli-input-json file://deploy/ecs/task-definition.ec2.json \
  --region "$AWS_REGION"
```

## 6. 创建 ECS Service

在 AWS Console 创建 Service：

- Cluster：`ith5`
- Launch type：EC2
- Task definition：`ith5`
- Desired tasks：`1`
- Load balancer：选择上一步 ALB target group
- Deployment minimum healthy percent：`0`
- Deployment maximum percent：`100`

因为 PostgreSQL 在同一个任务里，单实例部署时不要让新旧任务同时写同一个数据目录。

## 7. 验证

```sh
curl -fsSL "https://$DOMAIN/healthz"
curl -fsSL "https://$DOMAIN/install.sh" | head
```

浏览器打开：

```text
https://ith5.example.com
```

员工安装命令：

```sh
curl -fsSL https://ith5.example.com/install.sh | sh
ITH5_SERVER=https://ith5.example.com ith5 login
ith5 sync
```

## 8. 备份

Docker PostgreSQL 必须做备份。最小方案是在 EC2 上加 cron：

```sh
docker exec $(docker ps --filter name=postgres --format '{{.ID}}' | head -n1) \
  pg_dump -U ith5 ith5 > /opt/ith5/backup/ith5-$(date +%F).sql

aws s3 cp /opt/ith5/backup/ith5-$(date +%F).sql s3://你的备份桶/ith5/
```

生产更建议把备份脚本做成独立 ECS scheduled task，或者后续迁到 RDS。

## 9. 更新版本

每次发布：

```sh
export IMAGE_TAG=v1.0.1
docker build --build-arg VERSION="$IMAGE_TAG" -t "$ECR_REPO:$IMAGE_TAG" .
docker tag "$ECR_REPO:$IMAGE_TAG" "$AWS_ACCOUNT_ID.dkr.ecr.$AWS_REGION.amazonaws.com/$ECR_REPO:$IMAGE_TAG"
docker push "$AWS_ACCOUNT_ID.dkr.ecr.$AWS_REGION.amazonaws.com/$ECR_REPO:$IMAGE_TAG"
```

然后新建 task definition revision，更新 ECS Service。

## 10. 不要做的事

- 不要跑 `ith5-server -seed`
- 不要把 PostgreSQL 端口暴露公网
- 不要把 `ITH5_BASE_URL` 写成 ALB 内网地址
- 不要让两个 PostgreSQL 容器同时挂同一个数据目录
- 不要把生产 JWT secret 写进镜像或 git
