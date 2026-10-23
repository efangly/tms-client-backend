using System.Text.Json;
using Microsoft.Extensions.Logging.Abstractions;
using TmsBackend.Config;
using TmsBackend.Data;
using TmsBackend.Infrastructure;
using TmsBackend.Json;
using TmsBackend.Models;
using TmsBackend.Services;

namespace TmsBackend.Tests.Services;

internal sealed class FakeArchiveRepository : IArchiveRepository
{
    public Dictionary<string, List<TempLog>> DaysInTable { get; } = [];
    public HashSet<string> Archived { get; } = [];
    public List<ArchiveManifest> Manifests { get; } = [];
    public List<(string Table, string Day, bool Deleted)> Commits { get; } = [];
    public List<TempLog> ArchiveTable { get; private set; } = [];
    public Exception? ListError { get; set; }
    public string? LastCutoff { get; private set; }

    public Task<List<string>> ListDaysBeforeAsync(string cutoff, CancellationToken ct = default)
    {
        LastCutoff = cutoff;
        return ListError != null
            ? Task.FromException<List<string>>(ListError)
            : Task.FromResult(DaysInTable.Keys.Where(d => string.CompareOrdinal(d, cutoff[..10]) < 0).Order().ToList());
    }

    public Task<List<string>> ListDaysAsync(string table, CancellationToken ct = default) =>
        Task.FromResult(DaysInTable.Keys.Order().ToList());

    public Task<bool> ManifestExistsAsync(string day, CancellationToken ct = default) => Task.FromResult(Archived.Contains(day));

    public Task<List<TempLog>> GetDayRowsAsync(string table, string day, CancellationToken ct = default) =>
        Task.FromResult(DaysInTable.GetValueOrDefault(day, []));

    public Task CommitDayAsync(string table, string day, bool deleteRows, ArchiveManifest manifest, CancellationToken ct = default)
    {
        Commits.Add((table, day, deleteRows));
        Manifests.Add(manifest);
        Archived.Add(day);
        if (deleteRows)
            DaysInTable.Remove(day);
        return Task.CompletedTask;
    }

    public Task<List<ArchiveManifest>> ListManifestsAsync(string? startDate, string? endDate, CancellationToken ct = default) =>
        Task.FromResult(Manifests.Where(m => string.IsNullOrEmpty(startDate) || string.IsNullOrEmpty(endDate) || (string.CompareOrdinal(m.PeriodDate, startDate) >= 0 && string.CompareOrdinal(m.PeriodDate, endDate) <= 0)).ToList());

    public Task ReplaceArchiveRowsAsync(IReadOnlyList<TempLog> rows, CancellationToken ct = default)
    {
        ArchiveTable = [.. rows];
        return Task.CompletedTask;
    }
}

// Ported from internal/services/archive_service_test.go. One test changes the
// process working directory, so keep this class out of parallel runs.
[Collection(nameof(TmsBackend.Tests.Config.EnvironmentCollection))]
public class ArchiveServiceTests : IDisposable
{
    private readonly string _dir = Directory.CreateTempSubdirectory().FullName;
    private readonly FakeArchiveRepository _repo = new();
    private readonly ManualTimeProvider _time = ManualTimeProvider.AtThai(2026, 10, 22, 3, 0);

    public void Dispose() => Directory.Delete(_dir, true);

    private ArchiveService NewService(int runHour = 2) => new(_repo,
        new AppOptions { ArchiveDir = _dir, ArchiveRetentionDays = 30, ArchiveRunHour = runHour },
        new AppReadiness(), _time, NullLogger<ArchiveService>.Instance);

    private static List<TempLog> Rows(string day, int n) => Enumerable.Range(0, n).Select(i => new TempLog
    {
        MachineIP = "192.168.1.111",
        ProbeNo = 1,
        McuID = "A",
        TempValue = 4.5 + i,
        RealValue = 4450,
        Status = "N",
        InsertTime = DateTime.Parse(day).AddMinutes(i * 5),
        SDate = day.Replace("-", ""),
        STime = "00",
    }).ToList();

    [Fact]
    public async Task Writes_file_at_go_path_and_verifies()
    {
        var path = await ArchiveService.WriteArchiveFileAsync(_dir, "temp_log", "2026-08-27", Rows("2026-08-27", 3), default);

        Assert.Equal(Path.Join(_dir, "temp_log", "2026", "08", "temp_log_2026-08-27.json"), path);
        await ArchiveService.VerifyArchiveFileAsync(path, 3, default);
        await Assert.ThrowsAsync<InvalidDataException>(() => ArchiveService.VerifyArchiveFileAsync(path, 4, default));
    }

    [Fact]
    public async Task Relative_archive_dir_is_cleaned_like_go()
    {
        var cwd = Environment.CurrentDirectory;
        Environment.CurrentDirectory = _dir;
        try
        {
            var path = await ArchiveService.WriteArchiveFileAsync("./archives", "temp_log", "2026-08-27", Rows("2026-08-27", 1), default);
            Assert.Equal(@"archives\temp_log\2026\08\temp_log_2026-08-27.json", path);
        }
        finally
        {
            Environment.CurrentDirectory = cwd;
        }
    }

    [Fact]
    public async Task Invalid_day_and_missing_file_are_errors()
    {
        await Assert.ThrowsAsync<ArgumentException>(() => ArchiveService.WriteArchiveFileAsync(_dir, "temp_log", "2026-13-01", [], default));
        await Assert.ThrowsAsync<FileNotFoundException>(() => ArchiveService.VerifyArchiveFileAsync(Path.Join(_dir, "nope.json"), 1, default));
    }

    [Fact]
    public async Task Run_archive_archives_unarchived_days_before_cutoff()
    {
        _repo.DaysInTable["2026-09-01"] = Rows("2026-09-01", 3);
        _repo.DaysInTable["2026-09-02"] = Rows("2026-09-02", 2);
        _repo.DaysInTable["2026-10-20"] = Rows("2026-10-20", 5); // inside retention
        _repo.Archived.Add("2026-09-02");

        var result = await NewService().RunArchiveAsync();

        Assert.Equal(new ArchiveResult(1, 3), result);
        Assert.Equal("2026-09-22 00:00:00", _repo.LastCutoff);
        Assert.Equal([("temp_log", "2026-09-01", true)], _repo.Commits);
        var m = Assert.Single(_repo.Manifests);
        Assert.Equal(("temp_log", "2026-09-01", 3), (m.SourceTable, m.PeriodDate, m.RowCount));
        Assert.True(File.Exists(m.FilePath));
    }

    [Fact]
    public async Task Run_archive_with_nothing_to_do_returns_zero()
    {
        Assert.Equal(new ArchiveResult(0, 0), await NewService().RunArchiveAsync());
    }

    [Fact]
    public async Task Day_list_error_is_returned()
    {
        _repo.ListError = new InvalidOperationException("db down");
        await Assert.ThrowsAsync<InvalidOperationException>(() => NewService().RunArchiveAsync());
    }

    [Fact]
    public async Task Archive_from_backup_table_files_under_temp_log_without_deleting()
    {
        _repo.DaysInTable["2019-01-15"] = Rows("2019-01-15", 2);

        var result = await NewService().ArchiveFromTableAsync("temp_log_backup", deleteAfter: false);

        Assert.Equal(new ArchiveResult(1, 2), result);
        Assert.Equal([("temp_log_backup", "2019-01-15", false)], _repo.Commits);
        Assert.Equal("temp_log", _repo.Manifests[0].SourceTable);
        Assert.Contains("temp_log_2019-01-15.json", _repo.Manifests[0].FilePath);
        await Assert.ThrowsAsync<ArgumentException>(() => NewService().ArchiveFromTableAsync("x; DROP TABLE y", true));
    }

    [Fact]
    public async Task Restore_loads_files_into_archive_table_and_skips_missing()
    {
        _repo.DaysInTable["2026-09-01"] = Rows("2026-09-01", 3);
        var svc = NewService();
        await svc.RunArchiveAsync();
        _repo.Manifests.Add(new ArchiveManifest { SourceTable = "temp_log", PeriodDate = "2026-09-02", FilePath = Path.Join(_dir, "missing.json") });

        var result = await svc.RestoreRangeAsync("2026-09-01", "2026-09-30");

        Assert.Equal(new RestoreResult(1, 3), result);
        Assert.Equal(3, _repo.ArchiveTable.Count);
        Assert.Equal(new DateTime(2026, 9, 1, 0, 10, 0), _repo.ArchiveTable[2].InsertTime);
    }

    [Fact]
    public async Task Restore_with_no_manifests_clears_previous_data()
    {
        await _repo.ReplaceArchiveRowsAsync(Rows("2026-09-01", 2));

        var result = await NewService().RestoreRangeAsync("2020-01-01", "2020-01-31");

        Assert.Equal(new RestoreResult(0, 0), result);
        Assert.Empty(_repo.ArchiveTable);
    }

    [Fact]
    public async Task Restore_reads_files_written_by_go()
    {
        var path = Path.Join(_dir, "go.json");
        await File.WriteAllTextAsync(path, """[{"machineIp":"192.168.40.31","probeNo":1,"mcuId":"A","tempValue":7.22,"realValue":4722,"status":"00000000","sendTime":"2019-01-15T15:06:48+07:00","insertTime":"2019-01-15T15:06:49+07:00","sDate":"20190115","sTime":"15"}]""");
        _repo.Manifests.Add(new ArchiveManifest { SourceTable = "temp_log", PeriodDate = "2019-01-15", FilePath = path });

        await NewService().RestoreRangeAsync("2019-01-01", "2019-01-31");

        var row = Assert.Single(_repo.ArchiveTable);
        Assert.Equal((7.22, 4722), (row.TempValue!.Value, row.RealValue!.Value));
    }

    [Fact]
    public async Task Run_if_due_respects_hour_and_runs_once_per_day()
    {
        _repo.DaysInTable["2026-09-01"] = Rows("2026-09-01", 1);

        await NewService(runHour: 4).RunIfDueAsync(default); // 03:00 < 04:00
        Assert.Empty(_repo.Commits);

        var svc = NewService(runHour: 2);
        await svc.RunIfDueAsync(default);
        Assert.Single(_repo.Commits);

        _repo.DaysInTable["2026-09-02"] = Rows("2026-09-02", 1);
        await svc.RunIfDueAsync(default); // same day: skipped
        Assert.Single(_repo.Commits);

        _time.Advance(TimeSpan.FromDays(1));
        await svc.RunIfDueAsync(default);
        Assert.Equal(2, _repo.Commits.Count);
    }

    [Fact]
    public async Task Archive_file_json_matches_go_shape()
    {
        var path = await ArchiveService.WriteArchiveFileAsync(_dir, "temp_log", "2026-09-01", Rows("2026-09-01", 1), default);
        var json = await File.ReadAllTextAsync(path);
        var offset = GoTimeConverter.Format(new DateTime(2026, 9, 1))[19..];
        Assert.Equal($$"""[{"machineIp":"192.168.1.111","probeNo":1,"mcuId":"A","tempValue":4.5,"realValue":4450,"status":"N","sendTime":null,"insertTime":"2026-09-01T00:00:00{{offset}}","sDate":"20260901","sTime":"00"}]""", json);
        Assert.Single(JsonSerializer.Deserialize<List<TempLog>>(json, GoJson.Options)!);
    }
}
