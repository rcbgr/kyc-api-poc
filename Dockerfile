FROM public.ecr.aws/bitnami/golang:latest as builder

ARG CACHEBUST=1 

RUN mkdir -p /build
WORKDIR /build
COPY main.go .
COPY go.mod .
COPY go.sum .
COPY message.proto .

RUN wget https://github.com/protocolbuffers/protobuf/releases/download/v3.19.2/protoc-3.19.2-linux-x86_64.zip && unzip protoc-3.19.2-linux-x86_64.zip -d protoc

RUN go install google.golang.org/protobuf/cmd/protoc-gen-go@latest

RUN /build/protoc/bin/protoc -I=./ --go_out=./ ./message.proto

RUN CGO_ENABLED=0 GOOS=linux go build -a -installsuffix cgo -o main main.go

RUN openssl genrsa -out server.key 2048
RUN openssl ecparam -genkey -name secp384r1 -out server.key
RUN openssl req -new -x509 -sha256 -key server.key -out server.crt -days 3650 -subj "/C=US/ST=NYC/L=NYC/O=Global Security/OU=IT Department/CN=good.com"

FROM scratch

COPY --from=builder /build/main /main
COPY --from=builder /etc/passwd /etc/passwd
COPY --from=builder /etc/ssl/certs /etc/ssl/certs

COPY --from=builder /build/server.crt /server.crt
COPY --from=builder /build/server.key /server.key

EXPOSE 8443
CMD ["/main"]
