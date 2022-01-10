REGION ?= us-east-1
PROFILE ?= default
ENV_NAME ?= dev

ACCOUNT_ID := $(shell aws sts get-caller-identity --profile $(PROFILE) --query 'Account' --output text)

.PHONY: create-stack
create-stack:
	@aws cloudformation create-stack \
  --profile $(PROFILE) \
  --stack-name ky-ingestion-poc-$(ENV_NAME) \
  --region $(REGION) \
  --capabilities CAPABILITY_NAMED_IAM \
  --template-body file://infra/poc.cfn.yml \
	--parameters file://infra/poc.json

.PHONY: delete-stack
delete-stack:
	@aws cloudformation delete-stack \
  --profile $(PROFILE) \
  --stack-name ky-ingestion-poc-$(ENV_NAME) \
  --region $(REGION)

.PHONY: update-stack
update-stack:
	@aws cloudformation update-stack \
  --profile $(PROFILE) \
  --stack-name ky-ingestion-poc-$(ENV_NAME) \
  --region $(REGION) \
  --capabilities CAPABILITY_NAMED_IAM \
  --template-body file://infra/poc.cfn.yml \
	--parameters file://infra/poc.json

.PHONY: build
build:
	@docker build -t ky-ingestion-poc-$(ENV_NAME) .

.PHONY: update-service
update-service:
	aws ecr get-login-password \
	--profile $(PROFILE) \
	--region $(REGION) \
	| docker login --username AWS --password-stdin $(ACCOUNT_ID).dkr.ecr.$(REGION).amazonaws.com
	@docker build -t ky-ingestion-poc-$(ENV_NAME) .
	@docker tag ky-ingestion-poc-$(ENV_NAME):latest $(ACCOUNT_ID).dkr.ecr.$(REGION).amazonaws.com/ky-ingestion-poc-$(ENV_NAME):latest
	@docker push $(ACCOUNT_ID).dkr.ecr.$(REGION).amazonaws.com/ky-ingestion-poc-$(ENV_NAME):latest
	@aws ecs update-service \
	--profile $(PROFILE) \
	--region $(REGION) \
	--cluster ky-ingestion-poc-$(ENV_NAME) \
	--service ky-ingestion-poc-$(ENV_NAME) \
	--force-new-deployment


.PHONY: install-protoc
install-protoc:
	@brew install protobuf
	@go install google.golang.org/protobuf/cmd/protoc-gen-go@latest

.PHONY: protoc
protoc:
	@protoc -I=./ --go_out=./ ./message.proto
