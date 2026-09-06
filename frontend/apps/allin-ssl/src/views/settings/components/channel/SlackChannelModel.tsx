import { useForm, useModalHooks } from '@baota/naive-ui/hooks'
import { useError } from '@baota/hooks/error'
import { useSlackChannelFormController } from './useController'
import { useStore } from '@settings/useStore'

import type { ReportSlack, ReportType } from '@/types/setting'

export default defineComponent({
	name: 'SlackChannelModel',
	props: {
		data: {
			type: Object as PropType<ReportType<ReportSlack> | null>,
			default: () => null,
		},
	},
	setup(props: { data: ReportType<ReportSlack> | null }) {
		const { handleError } = useError()
		const { confirm } = useModalHooks()
		const { fetchNotifyChannels } = useStore()
		const { config, rules, slackChannelForm, submitForm } = useSlackChannelFormController()

		if (props.data) {
			const { name, config } = props.data
			slackChannelForm.value = {
				name,
				...config,
			}
		}

		const {
			component: SlackForm,
			example,
			data,
		} = useForm({
			config,
			defaultValue: slackChannelForm,
			rules,
		})

		confirm(async (close) => {
			try {
				const { name, ...other } = data.value
				await example.value?.validate()
				const result = await submitForm(
					{
						type: 'slack',
						name: name || '',
						config: other,
					},
					example,
					props.data?.id,
				)

				fetchNotifyChannels()
				if (result) close()
			} catch (error) {
				handleError(error)
			}
		})

		return () => (
			<div class="slack-channel-form">
				<SlackForm labelPlacement="top"></SlackForm>
			</div>
		)
	},
})
