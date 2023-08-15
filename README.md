# baton-box
`baton-box` is a connector for Box built using the [Baton SDK](https://github.com/conductorone/baton-sdk). It communicates with the Box API to sync data about users, groups and enterprise.

Check out [Baton](https://github.com/conductorone/baton) to learn more the project in general.

# Getting Started

## Prerequisites

1. Box `Custom App` created in [developer console](https://app.box.com/developers/console)
2. Authentication method set to `OAuth 2.0 with Client Credentials Grant (Server Authentication)`
3. App access level set to: `App + Enterprise Access`
4. Application Scopes: 
  - manage users
  - manage groups
  - manage enterprise properties
  - grant read resource
5. App must be approved by your Box admin. More info [here](https://developer.box.com/guides/authorization/custom-app-approval/)
6. Enterprise ID can be found in `Developer console -> Your App -> General settings`
7. Client ID and Client Secret can be found in `Developer console -> Your App -> Configuration`

## brew

```
brew install conductorone/baton/baton conductorone/baton/baton-box
baton-box
baton resources
```

## docker

```
docker run --rm -v $(pwd):/out -e BATON_BOX_CLIENT_ID=clientId BATON_BOX_CLIENT_SECRET=clientSecret BATON_ENTERPRISE_ID=enterpriseId ghcr.io/conductorone/baton-box:latest -f "/out/sync.c1z"
docker run --rm -v $(pwd):/out ghcr.io/conductorone/baton:latest -f "/out/sync.c1z" resources
```

## source

```
go install github.com/conductorone/baton/cmd/baton@main
go install github.com/conductorone/baton-box/cmd/baton-box@main

BATON_CLIENT_ID=clientId BATON_CLIENT_SECRET=clientSecret BATON_ENTERPRISE_ID=enterpriseId 
baton resources
```

# Data Model

`baton-box` pulls down information about the following Box resources:
- Users
- Groups
- Enterprise

# Contributing, Support, and Issues

We started Baton because we were tired of taking screenshots and manually building spreadsheets. We welcome contributions, and ideas, no matter how small -- our goal is to make identity and permissions sprawl less painful for everyone. If you have questions, problems, or ideas: Please open a Github Issue!

See [CONTRIBUTING.md](https://github.com/ConductorOne/baton/blob/main/CONTRIBUTING.md) for more details.

# `baton-box` Command Line Usage

```
baton-box

Usage:
  baton-box [flags]
  baton-box [command]

Available Commands:
  completion         Generate the autocompletion script for the specified shell
  help               Help about any command

Flags:
      --box-client-id string          Client ID used to authenticate to the Box API. ($BATON_BOX_CLIENT_ID)
      --box-client-secret string      Client Secret used to authenticate to the Box API. ($BATON_BOX_CLIENT_SECRET)
      --client-id string              The client ID used to authenticate with ConductorOne ($BATON_CLIENT_ID)
      --client-secret string          The client secret used to authenticate with ConductorOne ($BATON_CLIENT_SECRET)
      --enterprise-id string          ID of your Box enterprise. ($BATON_ENTERPRISE_ID)
  -f, --file string                   The path to the c1z file to sync with ($BATON_FILE) (default "sync.c1z")
  -h, --help                          help for baton-box
      --log-format string             The output format for logs: json, console ($BATON_LOG_FORMAT) (default "json")
      --log-level string              The log level: debug, info, warn, error ($BATON_LOG_LEVEL) (default "info")
  -v, --version                       version for baton-box

Use "baton-box [command] --help" for more information about a command.

```
