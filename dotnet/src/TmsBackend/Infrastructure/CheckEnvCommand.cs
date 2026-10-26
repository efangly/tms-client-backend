using TmsBackend.Config;
using TmsBackend.Data;

namespace TmsBackend.Infrastructure;

/// <summary>
/// `tms-backend.exe check-env`: pre-cutover check of a site's .env. Prints the
/// effective settings (secrets masked), invalid values, every setting that will
/// behave differently from the Go build, and whether the database answers.
/// Exit code 0 = nothing to review, 1 = review the listed items first.
/// </summary>
public static class CheckEnvCommand
{
    public static async Task<int> RunAsync(AppOptions options, string? envPath, IReadOnlyList<string> warnings)
    {
        Console.WriteLine($"TMS Backend {AppVersion.Value} - config check");
        Console.WriteLine($".env: {envPath ?? "(not found - using environment variables and defaults only)"}");
        Console.WriteLine();
        foreach (var (key, value) in Effective(options))
            Console.WriteLine($"  {key,-26} {value}");

        var review = 0;
        if (warnings.Count > 0)
        {
            Console.WriteLine("\nInvalid values (the default is used instead):");
            foreach (var w in warnings)
                Console.WriteLine($"  ! {w}");
            review += warnings.Count;
        }

        var diffs = GoDifferences(options);
        if (diffs.Count > 0)
        {
            Console.WriteLine("\nDifferent from the Go build (Go ignored these .env keys and always used the value on the right):");
            foreach (var d in diffs)
                Console.WriteLine($"  * {d}");
            review += diffs.Count;
        }

        Console.Write("\nDatabase: ");
        await using var db = new Db(options);
        using var timeout = new CancellationTokenSource(TimeSpan.FromSeconds(10));
        bool dbOk;
        try { dbOk = await db.PingAsync(timeout.Token); }
        catch (OperationCanceledException) { dbOk = false; }
        Console.WriteLine(dbOk ? $"OK ({options.DbHost}:{options.DbPort}/{options.DbName})" : $"NOT reachable ({options.DbHost}:{options.DbPort}/{options.DbName})");
        if (!dbOk)
            review++;

        Console.WriteLine(review == 0 ? "\nResult: OK" : $"\nResult: {review} item(s) to review");
        return review == 0 ? 0 : 1;
    }

    /// <summary>
    /// Settings whose effective value differs from what the Go build used. Go read
    /// POLL_INTERVAL/ALERT_INTERVAL as hardcoded 5m/5s and the DEFAULT_*/ARCHIVE_*
    /// keys in init(), before .env was loaded, so it always ran with the defaults.
    /// </summary>
    internal static List<string> GoDifferences(AppOptions o)
    {
        var go = new AppOptions();
        var list = new List<string>();
        void Check<T>(string key, T value, T goValue, Func<T, string>? fmt = null)
        {
            fmt ??= v => v?.ToString() ?? "";
            if (!EqualityComparer<T>.Default.Equals(value, goValue))
                list.Add($"{key}={fmt(value)} (Go: {fmt(goValue)})");
        }
        Check("POLL_INTERVAL", o.PollInterval, go.PollInterval, GoDuration.Format);
        Check("ALERT_INTERVAL", o.AlertInterval, go.AlertInterval, GoDuration.Format);
        Check("DEFAULT_TCP_PORT", o.DefaultTcpPort, go.DefaultTcpPort);
        Check("DEFAULT_SERIAL_BAUD", o.DefaultSerialBaud, go.DefaultSerialBaud);
        Check("DEFAULT_SERIAL_BOOT_DELAY", o.DefaultSerialBootDelay, go.DefaultSerialBootDelay, v => ((int)v.TotalSeconds).ToString());
        Check("ARCHIVE_DIR", o.ArchiveDir, go.ArchiveDir);
        Check("ARCHIVE_RETENTION_DAYS", o.ArchiveRetentionDays, go.ArchiveRetentionDays);
        Check("ARCHIVE_RUN_HOUR", o.ArchiveRunHour, go.ArchiveRunHour);
        if (!o.PollingAutostart)
            list.Add("POLLING_AUTOSTART=false - no automatic polling, alerts or archiving (test setting; Go always polled)");
        return list;
    }

    internal static IEnumerable<(string Key, string Value)> Effective(AppOptions o)
    {
        static string Mask(string s) => s == "" ? "(empty)" : "******";
        static string OrEmpty(string s) => s == "" ? "(empty)" : s;
        yield return ("PORT", o.Port.ToString());
        yield return ("DB_HOST", OrEmpty(o.DbHost));
        yield return ("DB_PORT", o.DbPort.ToString());
        yield return ("DB_USER", OrEmpty(o.DbUser));
        yield return ("DB_PASSWORD", Mask(o.DbPassword));
        yield return ("DB_NAME", OrEmpty(o.DbName));
        yield return ("POLL_INTERVAL", GoDuration.Format(o.PollInterval));
        yield return ("ALERT_INTERVAL", GoDuration.Format(o.AlertInterval));
        yield return ("POLLING_AUTOSTART", o.PollingAutostart ? "true" : "false");
        yield return ("DEFAULT_TCP_PORT", o.DefaultTcpPort.ToString());
        yield return ("DEFAULT_SERIAL_BAUD", o.DefaultSerialBaud.ToString());
        yield return ("DEFAULT_SERIAL_BOOT_DELAY", ((int)o.DefaultSerialBootDelay.TotalSeconds).ToString());
        yield return ("LEGACY_API_URL", o.LegacyApiUrl == "" ? "(empty - Legacy API off)" : o.LegacyApiUrl);
        yield return ("LEGACY_API_TOKEN", Mask(o.LegacyApiToken));
        yield return ("MQTT_BROKER", o.MqttBroker == "" ? "(empty - MQTT off)" : $"{o.MqttBroker}:{o.MqttPort}");
        yield return ("MQTT_CLIENT_ID", OrEmpty(o.MqttClientId));
        yield return ("MQTT_USERNAME", OrEmpty(o.MqttUsername));
        yield return ("MQTT_PASSWORD", Mask(o.MqttPassword));
        yield return ("MQTT_TOPIC", o.MqttTopic);
        yield return ("ARCHIVE_DIR", o.ArchiveDir);
        yield return ("ARCHIVE_RETENTION_DAYS", o.ArchiveRetentionDays.ToString());
        yield return ("ARCHIVE_RUN_HOUR", o.ArchiveRunHour.ToString());
    }
}
