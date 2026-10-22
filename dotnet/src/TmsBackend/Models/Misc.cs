using System.Text.Json.Serialization;

namespace TmsBackend.Models;

/// <summary>archive_manifest: which day of temp_log has been archived to which file.</summary>
public sealed class ArchiveManifest
{
    [JsonPropertyName("id")]
    public int ID { get; set; }

    [JsonPropertyName("sourceTable")]
    public string SourceTable { get; set => field = value ?? ""; } = "";

    /// <summary>YYYY-MM-DD.</summary>
    [JsonPropertyName("periodDate")]
    public string PeriodDate { get; set => field = value ?? ""; } = "";

    [JsonPropertyName("filePath")]
    public string FilePath { get; set => field = value ?? ""; } = "";

    [JsonPropertyName("rowCount")]
    public int RowCount { get; set; }

    [JsonPropertyName("archivedAt")]
    public DateTime ArchivedAt { get; set; }
}

/// <summary>config_value table (kept for parity; the backend does not query it).</summary>
public sealed class ConfigValue
{
    [JsonPropertyName("id")]
    public int ID { get; set; }

    [JsonPropertyName("configKey")]
    public string ConfigKey { get; set => field = value ?? ""; } = "";

    [JsonPropertyName("configValue")]
    public string? Value { get; set; }
}

/// <summary>master_user table (kept for parity; the backend does not query it).</summary>
public sealed class MasterUser
{
    [JsonPropertyName("id")]
    public int ID { get; set; }

    [JsonPropertyName("username")]
    public string Username { get; set => field = value ?? ""; } = "";

    [JsonIgnore]
    public string Password { get; set => field = value ?? ""; } = "";

    [JsonPropertyName("fullname")]
    public string? Fullname { get; set; }

    [JsonPropertyName("role")]
    public string? Role { get; set; }
}
