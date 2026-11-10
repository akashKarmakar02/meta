# Meta documentation

Meta is a Go web framework with server-rendered templ pages and client-rendered Svelte pages. This documentation describes the current `v0.1.0-beta.5` starter and public APIs. The project is a beta release, so pin the CLI and module versions and check release notes before upgrading.

## Start here

1. [Install Meta and create a project](getting-started/installation.md)
2. [Understand the project structure](getting-started/project-structure.md)
3. [Create pages and layouts](guides/routing.md)
4. [Add CSS](guides/styling.md)

## Guides

- [Environment variables](guides/environment.md)
- [Public files](guides/public-files.md)
- [Page titles and metadata](guides/metadata.md)
- [Create an API](guides/api.md)
- [Cross-origin resource sharing (CORS)](guides/cors.md)
- [Middleware and route policies](guides/middleware.md)
- [Clerk authentication (experimental)](guides/clerk.md)
- [Logs and metrics](guides/observability.md)
- [Caching: loaders, pages, sitemap](guides/caching.md)
- [Load data for a page](guides/loaders.md)
- [Forms and validation](guides/forms.md)
- [Build and deploy](guides/deployment.md)
- [Docker images](guides/docker.md)
- [Releases and migration](guides/releases.md)
- [Troubleshooting](guides/troubleshooting.md)

For the full reference and implementation details, see the [repository README](../README.md). Examples use `example.com/my-app` as the module path. Replace it with the path in your project's `go.mod`.
