using System.Globalization;

namespace TmsBackend.Config;

/// <summary>
/// Every setting the Go backend read via os.Getenv, with the same defaults.
/// Built once from the environment after .env has been loaded.
/// </summary>
public sealed record AppOptions
{
    public int Port { get; init; } = 8080;

    // Database
    public string DbHost { get; init; } = "";
    public int DbPort { get; init; } = 3306;
    public string DbUser { get; init; } = "";
    public string DbPassword { get; init; } = "";
    public string DbName { get; init; } = "";
    /// <summary>Logged only: MySqlConnector always talks utf8mb4 and the server converts per column (see spikes/README.md).</summary>
    public string DbCharset { get; init; } = "tis620";

    // Polling (Go hardcoded 5m / 5s and ignored these keys; C# honors them)
    public TimeSpan PollInterval { get; init; } = TimeSpan.FromMinutes(5);
    public TimeSpan AlertInterval { get; init; } = TimeSpan.FromSeconds(5);
    /// <summary>False disables automatic polling/archiving ticks (e2e runs trigger them over HTTP).</summary>
    public bool PollingAutostart { get; init; } = true;

    // Probe transports
    public int DefaultTcpPort { get; init; } = 8899;
    public int DefaultSerialBaud { get; init; } = 9600;
    public TimeSpan DefaultSerialBootDelay { get; init; } = TimeSpan.FromSeconds(2);

    // Legacy API
    public string LegacyApiUrl { get; init; } = "";
    public string LegacyApiToken { get; init; } = "";

    // MQTT
    public string MqttBroker { get; init; } = "";
    public string MqttPort { get; init; } = "1883";
    public string MqttClientId { get; init; } = "";
    public string MqttUsername { get; init; } = "";
    public string MqttPassword { get; init; } = "";
    public string MqttTopic { get; init; } = "tms/temperature";

    // Archive
    public string ArchiveDir { get; init; } = "./archives";
    public int ArchiveRetentionDays { get; init; } = 30;
    public int ArchiveRunHour { get; init; } = 2;

    /// <summary>Reads the current process environment, falling back to defaults for missing or invalid values.</summary>
    public static AppOptions FromEnvironment(Action<string>? warn = null)
    {
        var d = new AppOptions();

        string Str(string key, string def) =>
            Environment.GetEnvironmentVariable(key) is { Length: > 0 } v ? v : def;

        int Int(string key, int def, Func<int, bool>? valid = null)
        {
            var raw = Environment.GetEnvironmentVariable(key);
            if (string.IsNullOrEmpty(raw))
                return def;
            if (int.TryParse(raw, NumberStyles.Integer, CultureInfo.InvariantCulture, out var n) && (valid?.Invoke(n) ?? true))
                return n;
            warn?.Invoke($"Invalid {key}={raw}, using default {def}");
            return def;
        }

        TimeSpan Dur(string key, TimeSpan def)
        {
            var raw = Environment.GetEnvironmentVariable(key);
            if (string.IsNullOrEmpty(raw))
                return def;
            if (GoDuration.TryParse(raw, out var ts) && ts > TimeSpan.Zero)
                return ts;
            warn?.Invoke($"Invalid {key}={raw}, using default {def}");
            return def;
        }

        bool Bool(string key, bool def)
        {
            var raw = Environment.GetEnvironmentVariable(key);
            if (string.IsNullOrEmpty(raw))
                return def;
            if (bool.TryParse(raw, out var b))
                return b;
            if (raw is "1" or "0")
                return raw == "1";
            warn?.Invoke($"Invalid {key}={raw}, using default {def}");
            return def;
        }

        return new AppOptions
        {
            Port = Int("PORT", d.Port, n => n is > 0 and < 65536),
            DbHost = Str("DB_HOST", d.DbHost),
            DbPort = Int("DB_PORT", d.DbPort, n => n is > 0 and < 65536),
            DbUser = Str("DB_USER", d.DbUser),
            DbPassword = Str("DB_PASSWORD", d.DbPassword),
            DbName = Str("DB_NAME", d.DbName),
            DbCharset = Str("DB_CHARSET", d.DbCharset),
            PollInterval = Dur("POLL_INTERVAL", d.PollInterval),
            AlertInterval = Dur("ALERT_INTERVAL", d.AlertInterval),
            PollingAutostart = Bool("POLLING_AUTOSTART", d.PollingAutostart),
            DefaultTcpPort = Int("DEFAULT_TCP_PORT", d.DefaultTcpPort, n => n is > 0 and < 65536),
            DefaultSerialBaud = Int("DEFAULT_SERIAL_BAUD", d.DefaultSerialBaud, n => n > 0),
            // Seconds, as in serial_service.go
            DefaultSerialBootDelay = TimeSpan.FromSeconds(Int("DEFAULT_SERIAL_BOOT_DELAY", (int)d.DefaultSerialBootDelay.TotalSeconds, n => n >= 0)),
            LegacyApiUrl = Str("LEGACY_API_URL", d.LegacyApiUrl),
            LegacyApiToken = Str("LEGACY_API_TOKEN", d.LegacyApiToken),
            MqttBroker = Str("MQTT_BROKER", d.MqttBroker),
            MqttPort = Str("MQTT_PORT", d.MqttPort),
            MqttClientId = Str("MQTT_CLIENT_ID", d.MqttClientId),
            MqttUsername = Str("MQTT_USERNAME", d.MqttUsername),
            MqttPassword = Str("MQTT_PASSWORD", d.MqttPassword),
            MqttTopic = Str("MQTT_TOPIC", d.MqttTopic),
            ArchiveDir = Str("ARCHIVE_DIR", d.ArchiveDir),
            ArchiveRetentionDays = Int("ARCHIVE_RETENTION_DAYS", d.ArchiveRetentionDays, n => n > 0),
            ArchiveRunHour = Int("ARCHIVE_RUN_HOUR", d.ArchiveRunHour, n => n is >= 0 and < 24),
        };
    }
}
