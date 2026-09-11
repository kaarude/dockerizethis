# .NET detection

`dotnet.Detector{}` implements `detect.Detector` and reports stack `dotnet`.
It reads *.csproj/*.fsproj/*.vbproj with encoding/xml and discovers nested
projects through .sln entries. It does not run dotnet tooling. bin/, obj/,
out/, and .git are skipped when scanning sources.

The plan includes the target framework (first TargetFramework), package
references mapped to backing services (Npgsql, MySql, MongoDB,
StackExchange.Redis), literal `Environment.GetEnvironmentVariable` names,
the `dotnet publish` build command, and `dotnet /app/<assembly>.dll` as the
start command. The assembly name is AssemblyName when set, otherwise the
project filename.

An SDK of Microsoft.NET.Sdk.Web or an AspNetCore framework/package
reference makes the process web on port 8080 — or the first HTTP
applicationUrl port in Properties/launchSettings.json. A mapped `/health`
endpoint becomes HealthPath. Discord client packages (Discord.Net,
DSharpPlus, NetCord) name the framework without implying web.

A single project is required to complete the plan; several project files
produce an empty process with a note listing the candidates, which the CLI
reports as an undetermined process.
