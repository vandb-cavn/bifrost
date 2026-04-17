const NotAvailableBanner = () => {
	return (
		<div className="h-base flex items-center justify-center p-4">
			<div className="w-full max-w-md rounded-sm border border-red-200 bg-red-50 p-4 text-red-950 dark:border-red-900/40 dark:bg-red-950/20 dark:text-red-100">
				<div className="text-sm font-medium">Config store setup is missing.</div>
				<div className="mt-2 space-y-2 text-xs leading-5">
					<div>The UI requires a database connection to store configuration data, but no database is currently configured.</div>
					<div className="text-muted-foreground dark:text-red-200/80">
						To enable the UI, add the database settings to your `config.json` and follow the setup guide at{" "}
						<a
							href="https://www.getmaxim.ai/bifrost/docs/quickstart/gateway/setting-up#two-configuration-modes"
							target="_blank"
							rel="noopener noreferrer"
							className="font-medium underline underline-offset-2"
						>
							documentation
						</a>
						.
					</div>
				</div>
			</div>
		</div>
	);
};

export default NotAvailableBanner;
