# .NET detection

`dotnet.Detector{}` reads csproj/fsproj/vbproj XML and discovers nested projects
through .sln entries. Solution paths must remain inside the build context,
including through symbolic links. Detection does not run dotnet tooling.

The plan records the target framework, package services, environment variables,
assembly name, and publish/start commands. Multiple target frameworks select the
first and pass it explicitly to publish. Unsupported or platform-specific target
frameworks and library-only projects leave the process undetermined with a note.
Several project candidates also require the caller to select an executable.

Web SDK and ASP.NET references identify web projects. The port comes from the
selected project's Properties/launchSettings.json, with 8080 as the fallback.
The renderer binds that port through ASPNETCORE_URLS. Source scanning stays within
the selected project and skips build output and test paths.

Direct MapGet or MapHealthChecks declarations on the built WebApplication preserve
literal `/health...` paths. Comments, strings, POST handlers, controller attributes,
and group/prefix routing do not establish a GET health probe. Discord client
packages can identify a framework without implying a web process.
