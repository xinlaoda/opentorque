# Contributing to OpenTorque

Thank you for your interest in contributing to OpenTorque!

## How to Contribute

1. **Fork** the repository
2. **Create a branch** for your feature or fix (`git checkout -b feature/my-feature`)
3. **Make your changes** with clear, well-commented code
4. **Test** your changes (`make test`)
5. **Commit** with a descriptive message
6. **Push** and open a **Pull Request**

## Development Setup

```bash
# Clone your fork
git clone https://github.com/yourusername/opentorque.git
cd opentorque

# Build
make all

# Run tests
make test
```

## Code Style

- Follow standard Go conventions (`gofmt`, `go vet`)
- Comments in English
- Add comments to key functions and non-obvious logic
- Keep functions focused and reasonably sized

## Reporting Issues

- Use GitHub Issues
- Include: Go version, OS/arch, steps to reproduce, expected vs. actual behavior
- For security issues, email directly instead of opening a public issue

## License

By contributing, you agree that your contributions will be licensed under the Apache License 2.0.
