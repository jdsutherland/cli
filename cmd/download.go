package cmd

import (
	"fmt"

	"github.com/exercism/cli/config"
	"github.com/exercism/cli/service"
	"github.com/spf13/cobra"
	"github.com/spf13/pflag"
	"github.com/spf13/viper"
)

// downloadCmd represents the download command.
var downloadCmd = &cobra.Command{
	Use:     "download",
	Aliases: []string{"d"},
	Short:   "Download an exercise.",
	Long: `Download an exercise.

You may download an exercise to work on. If you've already
started working on it, the command will also download your
latest solution.

Download other people's solutions by providing the UUID.
`,
	RunE: func(cmd *cobra.Command, args []string) error {
		cfg := config.NewConfig()

		v := viper.New()
		v.AddConfigPath(cfg.Dir)
		v.SetConfigName("user")
		v.SetConfigType("json")
		// Ignore error. If the file doesn't exist, that is fine.
		_ = v.ReadInConfig()
		cfg.UserViperConfig = v

		return runDownload(cfg, cmd.Flags(), args)
	},
}

func runDownload(cfg config.Config, flags *pflag.FlagSet, args []string) error {
	if err := validateUserConfig(cfg.UserViperConfig); err != nil {
		return err
	}

	ctx, err := newDownloadCmdContext(cfg.UserViperConfig, flags)
	if err != nil {
		return err
	}

	if err = ctx.WriteSolutionFiles(); err != nil {
		return err
	}

	if err := ctx.WriteMetadata(); err != nil {
		return err
	}

	ctx.printResult()
	return nil
}

// downloadCmdContext is the context for downloadCmd.
type downloadCmdContext struct {
	usrCfg *viper.Viper
	flags  *pflag.FlagSet
	*service.DownloadWriter
}

// newDownloadCmdContext creates new downloadCmdContext,
// providing a download ready for work.
func newDownloadCmdContext(usrCfg *viper.Viper, flags *pflag.FlagSet) (*downloadCmdContext, error) {
	params, err := service.NewDownloadParamsFromFlags(usrCfg, flags)
	if err != nil {
		return nil, err
	}

	download, err := service.NewDownload(params)
	if err != nil {
		return nil, err
	}

	writer, err := service.NewDownloadWriter(download)
	if err != nil {
		return nil, err
	}

	return &downloadCmdContext{
		usrCfg:         usrCfg,
		flags:          flags,
		DownloadWriter: writer,
	}, nil
}

func (d *downloadCmdContext) printResult() {
	fmt.Fprintf(Err, "\nDownloaded to\n")
	fmt.Fprintf(Out, "%s\n", d.Exercise().MetadataDir())
}

func setupDownloadFlags(flags *pflag.FlagSet) {
	flags.StringP("uuid", "u", "", "the solution UUID")
	flags.StringP("track", "t", "", "the track ID")
	flags.StringP("exercise", "e", "", "the exercise slug")
	flags.StringP("team", "T", "", "the team slug")
}

func init() {
	RootCmd.AddCommand(downloadCmd)
	setupDownloadFlags(downloadCmd.Flags())
}
