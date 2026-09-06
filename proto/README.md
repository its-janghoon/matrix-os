# Matrix Proto

This repository contains the Protocol Buffer definitions for Matrix OS, a peer-to-peer orchestration system for autonomous AI agents. The protocol definitions enable standardized communication and interaction between AI agents, their environments, and the underlying infrastructure.

## Overview

Matrix OS uses Protocol Buffers to define:
- Soul (Agent) lifecycle and capabilities
- Inter-agent messaging and communication
- Memory and knowledge management
- Value systems and goal-oriented behavior
- Matrix (environment) interactions
- Resource management and sandboxing

## Repository Structure

```
.
├── matrix/
│   ├── common/v1/      # Common types and utilities
│   ├── soul/v1/        # Soul (agent) related definitions
│   │   ├── messaging.proto    # Agent communication
│   │   ├── lifecycle.proto    # Agent lifecycle management
│   │   └── memory.proto       # Memory and knowledge
│   └── matrix/v1/      # Matrix (environment) definitions
│       ├── interaction.proto  # P2P interactions
│       └── sandbox.proto      # Resource management
```

## Key Components

### Soul Service
- Agent lifecycle management (creation, training, updates)
- Stateless and streaming inference
- Bidirectional chat capabilities
- Training state management

### Memory Service
- Memory storage and retrieval
- Value system management
- Goal tracking and updates
- Context-aware memory queries

### Matrix Service
- P2P message routing
- Event observation and streaming
- Emergent signal detection
- Resource policy enforcement

### Sandbox Service
- Resource limits and quotas
- Network access controls
- Storage policies
- Computation constraints
- Interaction rules

## Usage

1. Install the Protocol Buffer compiler (protoc)
2. Install language-specific plugins as needed
3. Use `buf` for linting and generation:

```bash
# Lint proto files
buf lint

# Generate code
buf generate
```

## Best Practices

1. Follow standard Protocol Buffer naming conventions
2. Use appropriate response types for all RPCs
3. Include proper documentation for messages and fields
4. Maintain backward compatibility when making changes
5. Use common types from matrix/common/v1

## Contributing

1. Fork the repository
2. Create a feature branch
3. Make your changes following the style guide
4. Run linting and tests
5. Submit a pull request

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.