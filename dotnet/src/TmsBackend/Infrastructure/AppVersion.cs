using System.Reflection;

namespace TmsBackend.Infrastructure;

/// <summary>Build version baked in by `dotnet publish -p:Version=...` (Go: -X version.Version).</summary>
public static class AppVersion
{
    public static string Value { get; } =
        typeof(AppVersion).Assembly.GetCustomAttribute<AssemblyInformationalVersionAttribute>()?.InformationalVersion
        ?? "dev";
}
