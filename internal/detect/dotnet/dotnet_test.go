package dotnet_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/carl/dockerizethis/internal/detect/dotnet"
	"github.com/carl/dockerizethis/internal/plan"
	"github.com/stretchr/testify/require"
)

func TestFixtures(t *testing.T) {
	for _, tc := range []struct {
		name string
		want plan.Plan
	}{
		{"dotnet-web", plan.Plan{Stack: "dotnet", Version: "8.0", PkgManager: "dotnet", Framework: "aspnet", Process: plan.ProcessWeb, Port: 8080, HealthPath: "/health", BuildCmd: "dotnet publish app.csproj -c Release -o /app/out", StartCmd: "dotnet /app/app.dll", Workdir: "/app", Confidence: 0.9, Extras: map[string]string{"projectFile": "app.csproj"}}},
		{"dotnet-worker", plan.Plan{Stack: "dotnet", Version: "8.0", PkgManager: "dotnet", Process: plan.ProcessWorker, Env: []plan.EnvVar{{Name: "DISCORD_TOKEN", Required: true}}, BuildCmd: "dotnet publish worker.csproj -c Release -o /app/out", StartCmd: "dotnet /app/worker.dll", Workdir: "/app", Confidence: 0.8, Extras: map[string]string{"projectFile": "worker.csproj"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p, ok, err := (dotnet.Detector{}).Detect(filepath.Join("..", "..", "..", "testdata", "fixtures", tc.name))
			require.NoError(t, err)
			require.True(t, ok)
			require.Equal(t, tc.want, p)
		})
	}
	require.Equal(t, "dotnet", (dotnet.Detector{}).Name())
}

func TestPackagesAndLaunchSettings(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "Shop.csproj", `<Project Sdk="Microsoft.NET.Sdk.Web">
  <PropertyGroup>
    <TargetFramework>net9.0</TargetFramework>
    <AssemblyName>shop-api</AssemblyName>
  </PropertyGroup>
  <ItemGroup>
    <PackageReference Include="Npgsql.EntityFrameworkCore.PostgreSQL" Version="9.0" />
    <PackageReference Include="StackExchange.Redis" Version="2.8" />
    <PackageReference Include="MongoDB.Driver" Version="3.0" />
    <PackageReference Include="MySqlConnector" Version="2.4" />
  </ItemGroup>
</Project>`)
	write(t, dir, "Properties/launchSettings.json", `{"profiles":{"http":{"applicationUrl":"http://localhost:5217"}}}`)
	write(t, dir, "Program.cs", `var app = WebApplication.CreateBuilder(args).Build();
app.MapGet("/health", () => "ok");
app.Run();`)
	p, ok, err := (dotnet.Detector{}).Detect(dir)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "9.0", p.Version)
	require.Equal(t, 5217, p.Port, "launchSettings.json applicationUrl wins over the default")
	require.Equal(t, "dotnet /app/shop-api.dll", p.StartCmd, "AssemblyName overrides the project filename")
	require.Equal(t, "dotnet publish Shop.csproj -c Release -o /app/out", p.BuildCmd)
	require.Equal(t, []plan.Service{plan.ServiceMongo, plan.ServiceMySQL, plan.ServicePostgres, plan.ServiceRedis}, p.Services)
}

func TestSolutionAndAmbiguity(t *testing.T) {
	dir := t.TempDir()
	write(t, dir, "App.sln", "Microsoft Visual Studio Solution File\nProject(\"{SDK}\") = \"Api\", \"src\\Api\\Api.csproj\", \"{G1}\"\nEndProject\n")
	write(t, dir, "src/Api/Api.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework><OutputType>Exe</OutputType></PropertyGroup></Project>`)
	p, ok, err := (dotnet.Detector{}).Detect(dir)
	require.NoError(t, err)
	require.True(t, ok)
	require.Equal(t, "src/Api/Api.csproj", p.Extras["projectFile"], "Windows path separators normalize")

	write(t, dir, "Lib.csproj", `<Project Sdk="Microsoft.NET.Sdk"><PropertyGroup><TargetFramework>net8.0</TargetFramework><OutputType>Library</OutputType></PropertyGroup></Project>`)
	p, ok, err = (dotnet.Detector{}).Detect(dir)
	require.NoError(t, err)
	require.True(t, ok)
	require.LessOrEqual(t, p.Confidence, 0.4)
	require.Empty(t, p.StartCmd)
	require.Contains(t, p.Notes[0], "multiple project files")
}

func TestAbsentAndFailures(t *testing.T) {
	dir := t.TempDir()
	p, ok, err := (dotnet.Detector{}).Detect(dir)
	require.NoError(t, err)
	require.False(t, ok)
	require.Equal(t, plan.Plan{}, p)
	write(t, dir, "Broken.csproj", "<Project>")
	_, ok, err = (dotnet.Detector{}).Detect(dir)
	require.ErrorContains(t, err, "parse Broken.csproj")
	require.False(t, ok)
}

func write(t *testing.T, dir, name, data string) {
	t.Helper()
	filename := filepath.Join(dir, name)
	require.NoError(t, os.MkdirAll(filepath.Dir(filename), 0o755))
	require.NoError(t, os.WriteFile(filename, []byte(data), 0o644))
}
