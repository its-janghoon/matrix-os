# Contributing to Matrix Proto

We love your input! We want to make contributing to Matrix Proto as easy and transparent as possible, whether it's:

- Reporting a bug
- Discussing the current state of the code
- Submitting a fix
- Proposing new features
- Becoming a maintainer

## Development Process

We use GitHub to host code, to track issues and feature requests, as well as accept pull requests.

1. Fork the repo and create your branch from `main`
2. Make your changes and ensure they follow our Protocol Buffer style guide
3. Run `buf lint` to check for any style violations
4. Run `buf generate` to ensure your changes compile correctly
5. Update documentation if you've changed APIs
6. Issue that pull request!

## Protocol Buffer Style Guide

### Naming Conventions

#### Files
- Use `snake_case` for file names
- Group related messages in a single file
- Name files after their primary service or message type
- Example: `node_control.proto`, `metrics.proto`

#### Packages
- Use the format: `matrix.<component>.v1`
- Version number must be included in the package name
- Example: `package matrix.agent.v1;`

#### Messages
- Use `PascalCase` for message names
- Request messages must end with `Request`
- Response messages must end with `Response`
- Example: `HealthCheckRequest`, `HealthCheckResponse`

#### Fields
- Use `snake_case` for field names
- Boolean fields should start with verbs
- Example: `is_active`, `has_metadata`, `should_retry`

#### Services
- Use `PascalCase` and end with `Service`
- Example: `NodeControlService`, `MetricsService`

#### Methods (RPCs)
- Use `PascalCase`
- Follow verb-noun pattern
- Example: `GetMetrics`, `StreamLogs`, `UpdateConfig`

### Field Numbers and Types

1. Reserve field numbers 1-15 for frequently occurring elements
2. Use appropriate types:
   - `bytes` for binary data
   - `string` for text
   - `int64`/`uint64` for large numbers
   - `int32`/`uint32` for small numbers
   - `repeated` for lists
   - `map` for key-value pairs

### Documentation

1. Every service must have a comment describing its purpose
2. Every RPC method must have a comment describing:
   - What it does
   - Expected input
   - Expected output
   - Any side effects
3. All enums must have comments explaining possible values
4. Complex fields should have inline comments

Example:
```protobuf
// NodeControlService provides administrative operations for Matrix nodes.
service NodeControlService {
    // HealthCheck performs a health check on the node.
    // Returns detailed status of all subsystems if deep=true.
    rpc HealthCheck(HealthCheckRequest) returns (HealthCheckResponse) {}
}
```

### Breaking Changes

Avoid breaking changes by:
1. Never removing fields (mark as deprecated instead)
2. Never reusing field numbers
3. Never changing field types
4. Adding new fields as optional
5. Using reserved fields and names

Example:
```protobuf
message ExampleMessage {
    reserved 2, 15, 9 to 11;
    reserved "old_field", "temp_field";
    
    string current_field = 1;
    // string old_field = 2;  // Removed in v1.1
}
```

## Pull Request Process

1. Update the README.md with details of changes to the interface
2. Update the version numbers in examples to the new version that this Pull Request would represent
3. You may merge the Pull Request once you have the sign-off of two other developers

## Issue Reporting

### Security Issues

If you find a security vulnerability, do NOT open an issue. Email [SECURITY_EMAIL] instead.

### Bug Reports

When filing an issue, make sure to answer these questions:

1. What version of buf are you using?
2. What did you do?
3. What did you expect to see?
4. What did you see instead?

### Feature Requests

If you find yourself wishing for a feature that doesn't exist, you are probably not alone. Open an issue on our issues list on GitHub which describes:

1. The feature you would like to see
2. Why you need it
3. How it should work

## Community

- Respect our Code of Conduct
- Be welcoming to newcomers
- Keep discussions focused on improving the project

## License

By contributing, you agree that your contributions will be licensed under its MIT License. 