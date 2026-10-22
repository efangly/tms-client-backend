using TmsBackend.Config;
using TmsBackend.Data;

namespace TmsBackend.Tests.Data;

/// <summary>
/// Read-only checks against a real database. Opt-in: set TMS_DB_SMOKE to the
/// path of a .env file (e.g. the repo's .env); otherwise each test returns
/// immediately.
/// </summary>
[Collection(nameof(TmsBackend.Tests.Config.EnvironmentCollection))]
public class DbSmokeTests
{
    private static Db? Open()
    {
        var envPath = Environment.GetEnvironmentVariable("TMS_DB_SMOKE");
        if (string.IsNullOrEmpty(envPath))
            return null;
        DotNetEnv.Env.NoClobber().Load(envPath);
        return new Db(AppOptions.FromEnvironment());
    }

    [Fact]
    public async Task Reads_machines_latest_readings_and_logs()
    {
        await using var db = Open();
        if (db == null)
            return;

        Assert.True(await db.PingAsync());

        var machines = await new MachineRepository(db).GetAllAsync();
        Assert.NotEmpty(machines);
        Assert.All(machines, m => Assert.NotEqual("", m.MachineIP));

        var latest = await new MachineRepository(db).GetLatestReadingsAsync();
        Assert.All(latest, l => Assert.True(l.InsertTime > DateTime.MinValue));

        var logs = await new TempLogRepository(db).QueryAsync(new TempLogQuery { Descending = true, Limit = 20 });
        Assert.All(logs, l =>
        {
            // FLOAT normalization: no 25.399999618530273-style values for 2-decimal readings.
            if (l.TempValue is { } v)
                Assert.Equal(Math.Round(v, 2), v);
        });

        var first = machines[0];
        var device = DeviceFilter.Parse(first.MachineIP, machines);
        var filtered = await new TempLogRepository(db).QueryAsync(new TempLogQuery { Devices = device, Descending = true, Limit = 5 });
        Assert.All(filtered, l => Assert.Equal(first.MachineIP, l.MachineIP));

        await new TempErrorRepository(db).QueryAsync(null, null, 5);
        var manifests = await new ArchiveRepository(db).ListManifestsAsync(null, null);
        Assert.All(manifests, m => Assert.Equal("temp_log", m.SourceTable));
    }
}
