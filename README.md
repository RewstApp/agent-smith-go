# Agent Smith
[![Test](https://github.com/RewstApp/agent-smith-go/actions/workflows/test.yml/badge.svg)](https://github.com/RewstApp/agent-smith-go/actions/workflows/test.yml)
[![CodeQL](https://github.com/RewstApp/agent-smith-go/actions/workflows/github-code-scanning/codeql/badge.svg)](https://github.com/RewstApp/agent-smith-go/actions/workflows/github-code-scanning/codeql)
[![Release](https://github.com/RewstApp/agent-smith-go/actions/workflows/release.yml/badge.svg)](https://github.com/RewstApp/agent-smith-go/actions/workflows/release.yml)

Rewst's lean, open-source command executor that fits right into your Rewst workflows. See [community corner](https://docs.rewst.help/documentation/agent-smith) for more details.

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

Run the following command using `powershell` or `pwsh` to build the binary:
```
./scripts/build.ps1
```

## Contributing
Contributions are always welcome. Please submit a PR!

Please use commitizen to format the commit messages. After staging your changes, you can commit the changes with this command.

```
cz commit
```

## License

Agent Smith is licensed under `GNU GENERAL PUBLIC LICENSE`. See license file for details.