# Agent Smith
This is an implementation of agent smith using Golang.

## Build
Required tools and packages:

- [commitizen](https://commitizen-tools.github.io/commitizen/): To use a standardized description of commits.
  ```
  pipx ensurepath
  pipx install commitizen
  pipx upgrade commitizen
  ```

- [go-winres](https://github.com/tc-hib/go-winres): To embed icons and file versions to windows executables.
  ```
  go install github.com/tc-hib/go-winres@latest
  ```

## Directory Structure
```
agent-smith-go/
├── cmd/                  # Main applications of the project
│   ├── app1/
│   │   └── main.go       # Entry point for application 1
│   └── app2/
│       └── main.go       # Entry point for application 2
├── pkg/                  # Public libraries and reusable code
│   ├── module1/
│   │   ├── module1.go
│   │   └── module1_test.go
│   └── module2/
│       ├── module2.go
│       └── module2_test.go
├── internal/             # Private application and library code
│   ├── service1/
│   │   ├── service1.go
│   │   └── service1_test.go
│   └── service2/
│       ├── service2.go
│       └── service2_test.go
├── configs/              # Configuration files (e.g., JSON, YAML, etc.)
│   ├── app1-config.yaml
│   └── app2-config.yaml
├── scripts/              # Scripts for automation and build
│   ├── build.sh
│   ├── deploy.sh
│   └── test.sh
├── api/                  # API definition files (e.g., OpenAPI/Swagger, gRPC)
│   ├── proto/
│   │   ├── service1.proto
│   │   └── service2.proto
│   └── swagger.yaml
├── docs/                 # Documentation
│   ├── README.md
│   ├── DESIGN.md
│   └── API.md
├── tests/                # Integration or end-to-end tests
│   ├── test1.go
│   └── test2.go
├── vendor/               # Dependency management (used with `go mod vendor`)
├── .gitignore            # Git ignore file
├── go.mod                # Go module file
├── go.sum                # Go checksum file
└── README.md             # Project overview and instructions
```
