using TmsBackend.Config;

namespace TmsBackend.Tests.Config;

// Process environment is global state: keep these tests out of parallel runs.
[Collection(nameof(EnvironmentCollection))]
public class AppOptionsTests : IDisposable
{
    private static readonly string[] Keys =
    [
        "PORT", "DB_PORT", "POLL_INTERVAL", "ALERT_INTERVAL", "POLLING_AUTOSTART", "LOG_STREAM_ENABLED",
        "DEFAULT_SERIAL_BOOT_DELAY", "ARCHIVE_RUN_HOUR", "MQTT_TOPIC", "TMS_TEST_ENV_KEY",
    ];

    private readonly Dictionary<string, string?> _saved = Keys.ToDictionary(k => k, Environment.GetEnvironmentVariable);

    public AppOptionsTests()
    {
        foreach (var k in Keys)
            Environment.SetEnvironmentVariable(k, null);
    }

    public void Dispose()
    {
        foreach (var (k, v) in _saved)
            Environment.SetEnvironmentVariable(k, v);
    }

    [Fact]
    public void Defaults_match_go()
    {
        var o = AppOptions.FromEnvironment();
        Assert.Equal(8080, o.Port);
        Assert.Equal(3306, o.DbPort);
        Assert.Equal("tis620", o.DbCharset);
        Assert.Equal(TimeSpan.FromMinutes(5), o.PollInterval);
        Assert.Equal(TimeSpan.FromSeconds(5), o.AlertInterval);
        Assert.True(o.PollingAutostart);
        Assert.True(o.LogStreamEnabled);
        Assert.Equal(8899, o.DefaultTcpPort);
        Assert.Equal(9600, o.DefaultSerialBaud);
        Assert.Equal(TimeSpan.FromSeconds(2), o.DefaultSerialBootDelay);
        Assert.Equal("1883", o.MqttPort);
        Assert.Equal("tms/temperature", o.MqttTopic);
        Assert.Equal("./archives", o.ArchiveDir);
        Assert.Equal(30, o.ArchiveRetentionDays);
        Assert.Equal(2, o.ArchiveRunHour);
    }

    [Fact]
    public void Reads_environment()
    {
        Environment.SetEnvironmentVariable("PORT", "9090");
        Environment.SetEnvironmentVariable("POLL_INTERVAL", "1m");
        Environment.SetEnvironmentVariable("POLLING_AUTOSTART", "false");
        Environment.SetEnvironmentVariable("LOG_STREAM_ENABLED", "false");
        Environment.SetEnvironmentVariable("DEFAULT_SERIAL_BOOT_DELAY", "3");

        var o = AppOptions.FromEnvironment();

        Assert.Equal(9090, o.Port);
        Assert.Equal(TimeSpan.FromMinutes(1), o.PollInterval);
        Assert.False(o.PollingAutostart);
        Assert.False(o.LogStreamEnabled);
        Assert.Equal(TimeSpan.FromSeconds(3), o.DefaultSerialBootDelay);
    }

    [Fact]
    public void Invalid_values_fall_back_with_warning()
    {
        Environment.SetEnvironmentVariable("PORT", "abc");
        Environment.SetEnvironmentVariable("ALERT_INTERVAL", "5");
        Environment.SetEnvironmentVariable("ARCHIVE_RUN_HOUR", "24");
        var warnings = new List<string>();

        var o = AppOptions.FromEnvironment(warnings.Add);

        Assert.Equal(8080, o.Port);
        Assert.Equal(TimeSpan.FromSeconds(5), o.AlertInterval);
        Assert.Equal(2, o.ArchiveRunHour);
        Assert.Equal(3, warnings.Count);
    }

    [Fact]
    public void Env_file_does_not_override_real_environment()
    {
        var dir = Directory.CreateTempSubdirectory().FullName;
        try
        {
            File.WriteAllText(Path.Combine(dir, ".env"), "PORT=7000\nTMS_TEST_ENV_KEY=from-file\n");
            Environment.SetEnvironmentVariable("PORT", "9191");

            var loaded = EnvFile.Load(Path.Combine(dir, "missing"), dir);

            Assert.Equal(Path.Combine(dir, ".env"), loaded);
            Assert.Equal("9191", Environment.GetEnvironmentVariable("PORT"));
            Assert.Equal("from-file", Environment.GetEnvironmentVariable("TMS_TEST_ENV_KEY"));
        }
        finally
        {
            Directory.Delete(dir, true);
        }
    }
}

[CollectionDefinition(nameof(EnvironmentCollection), DisableParallelization = true)]
public class EnvironmentCollection;
