using TmsBackend.Config;
using TmsBackend.Infrastructure;

namespace TmsBackend.Tests.Config;

public class CheckEnvCommandTests
{
    [Fact]
    public void Defaults_match_go()
    {
        Assert.Empty(CheckEnvCommand.GoDifferences(new AppOptions()));
    }

    [Fact]
    public void Lists_every_setting_go_ignored()
    {
        var diffs = CheckEnvCommand.GoDifferences(new AppOptions
        {
            PollInterval = TimeSpan.FromMinutes(1),
            AlertInterval = TimeSpan.FromSeconds(10),
            DefaultTcpPort = 9000,
            ArchiveRetentionDays = 90,
            PollingAutostart = false,
        });

        Assert.Equal(
        [
            "POLL_INTERVAL=1m (Go: 5m)",
            "ALERT_INTERVAL=10s (Go: 5s)",
            "DEFAULT_TCP_PORT=9000 (Go: 8899)",
            "ARCHIVE_RETENTION_DAYS=90 (Go: 30)",
            "POLLING_AUTOSTART=false - no automatic polling, alerts or archiving (test setting; Go always polled)",
        ], diffs);
    }

    [Fact]
    public void Secrets_are_masked()
    {
        var values = CheckEnvCommand.Effective(new AppOptions { DbPassword = "pw", LegacyApiToken = "tok", MqttPassword = "" })
            .ToDictionary(p => p.Key, p => p.Value);

        Assert.Equal("******", values["DB_PASSWORD"]);
        Assert.Equal("******", values["LEGACY_API_TOKEN"]);
        Assert.Equal("(empty)", values["MQTT_PASSWORD"]);
        Assert.DoesNotContain(values.Values, v => v.Contains("pw") || v.Contains("tok"));
    }
}
