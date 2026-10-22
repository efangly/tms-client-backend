using System.Text.Json;

namespace TmsBackend.Data;

/// <summary>Raised when a partial update names a field that is not a master_machine column (Go: DB error → 500).</summary>
public sealed class UnknownColumnException(string key) : Exception($"unknown column '{key}' in field list");

/// <summary>
/// Turns a PUT body for a machine into column → value updates, reproducing
/// GORM's Updates(map) as used by UpdateDevice/UpdateMachine in handlers.go.
/// </summary>
public static class MachineUpdate
{
    // JSON names whose DB column differs (jsonToColumn in handlers.go).
    private static readonly Dictionary<string, string> JsonToColumn = new()
    {
        ["probeAll"] = "probe_all",
        ["machineName"] = "machine_name",
        ["minTemp"] = "min_temp",
        ["maxTemp"] = "max_temp",
        ["adjTemp"] = "adj_temp",
    };

    // GORM resolves a map key against Go field names (case-sensitive)...
    private static readonly Dictionary<string, string> GoFieldToColumn = new()
    {
        ["MachineIP"] = "machine_ip",
        ["ProbeNo"] = "probe_no",
        ["ProbeAll"] = "probe_all",
        ["MachineName"] = "machine_name",
        ["Color"] = "color",
        ["ChkOnline"] = "chkOnline",
        ["ChkSms"] = "chkSms",
        ["ChkMail"] = "chkMail",
        ["ChkMon"] = "chkMon",
        ["ChkLine"] = "chkLine",
        ["ChkReport"] = "chkReport",
        ["MinTemp"] = "min_temp",
        ["MaxTemp"] = "max_temp",
        ["AdjTemp"] = "adj_temp",
        ["SType"] = "sType",
    };

    // ...and otherwise uses it as a literal column name, which MySQL matches case-insensitively.
    private static readonly Dictionary<string, string> Columns =
        GoFieldToColumn.Values.ToDictionary(c => c, c => c, StringComparer.OrdinalIgnoreCase);

    private static readonly string[] PrimaryKeyKeys = ["machine_ip", "machineIp", "probe_no", "probeNo"];

    /// <summary>Column → value updates. Throws <see cref="UnknownColumnException"/> for keys that are not columns.</summary>
    public static Dictionary<string, object?> ToColumns(IReadOnlyDictionary<string, JsonElement> body)
    {
        var result = new Dictionary<string, object?>();
        foreach (var (key, value) in body)
        {
            // Prevent primary key mutation (both JSON and column spellings).
            if (PrimaryKeyKeys.Contains(key))
                continue;

            var name = JsonToColumn.GetValueOrDefault(key, key);
            if (!GoFieldToColumn.TryGetValue(name, out var column) && !Columns.TryGetValue(name, out column))
                throw new UnknownColumnException(name);

            result[column] = ToValue(value, name);
        }
        return result;
    }

    private static object? ToValue(JsonElement e, string key) => e.ValueKind switch
    {
        JsonValueKind.Null => null,
        JsonValueKind.String => e.GetString(),
        JsonValueKind.True => true,
        JsonValueKind.False => false,
        // encoding/json decodes every number into float64.
        JsonValueKind.Number => e.GetDouble(),
        _ => throw new UnknownColumnException($"{key} (unsupported value)"),
    };
}
