module github.com/JohnnyMatteus/payments-events-go/tools

go 1.26.0

tool (
	google.golang.org/grpc/cmd/protoc-gen-go-grpc
	google.golang.org/protobuf/cmd/protoc-gen-go
)

require (
	google.golang.org/grpc/cmd/protoc-gen-go-grpc v1.6.0 // indirect
	google.golang.org/protobuf v1.36.12 // indirect
)
