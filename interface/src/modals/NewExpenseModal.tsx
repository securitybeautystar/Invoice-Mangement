import React, { useRef } from 'react';
import NiceModal, { useModal } from '@ebay/nice-modal-react';
import { AxiosError } from 'axios';
import { startOfDay, startOfTomorrow } from 'date-fns';
import { FormikHelpers } from 'formik';
import { useSnackbar } from 'notistack';

import MAmountField from '@monetr/interface/components/MAmountField';
import MFormButton from '@monetr/interface/components/MButton';
import MDatePicker from '@monetr/interface/components/MDatePicker';
import MForm from '@monetr/interface/components/MForm';
import MModal, { MModalRef } from '@monetr/interface/components/MModal';
import MSelectFrequency from '@monetr/interface/components/MSelectFrequency';
import MSelectFunding from '@monetr/interface/components/MSelectFunding';
import MSpan from '@monetr/interface/components/MSpan';
import MTextField from '@monetr/interface/components/MTextField';
import { useSelectedBankAccountId } from '@monetr/interface/hooks/bankAccounts';
import { useCreateSpending } from '@monetr/interface/hooks/spending';
import Spending, { SpendingType } from '@monetr/interface/models/Spending';
import { friendlyToAmount } from '@monetr/interface/util/amounts';
import { ExtractProps } from '@monetr/interface/util/typescriptEvils';

interface NewExpenseValues {
  name: string;
  amount: number;
  nextOccurrence: Date;
  ruleset: string;
  fundingScheduleId: number;
}

const initialValues: NewExpenseValues = {
  name: '',
  amount: 0.00,
  nextOccurrence: startOfTomorrow(),
  ruleset: '',
  fundingScheduleId: 0,
};

function NewExpenseModal(): JSX.Element {
  const modal = useModal();
  const { enqueueSnackbar } = useSnackbar();
  const selectedBankAccountId = useSelectedBankAccountId();
  const createSpending = useCreateSpending();

  const ref = useRef<MModalRef>(null);

  async function submit(
    values: NewExpenseValues,
    helper: FormikHelpers<NewExpenseValues>,
  ): Promise<void> {
    const newSpending = new Spending({
      bankAccountId: selectedBankAccountId,
      name: values.name.trim(),
      nextRecurrence: startOfDay(new Date(values.nextOccurrence)),
      spendingType: SpendingType.Expense,
      fundingScheduleId: values.fundingScheduleId,
      targetAmount: friendlyToAmount(values.amount),
      ruleset: values.ruleset,
    });

    helper.setSubmitting(true);
    return createSpending(newSpending)
      .then(created => modal.resolve(created))
      .then(() => modal.remove())
      .catch((error: AxiosError) => void enqueueSnackbar(error.response.data['error'], {
        variant: 'error',
        disableWindowBlurListener: true,
      }))
      .finally(() => helper.setSubmitting(false));
  }

  return (
    <MModal open={ modal.visible } ref={ ref } className='sm:max-w-xl'>
      <MForm
        onSubmit={ submit }
        initialValues={ initialValues }
        className='h-full flex flex-col gap-2 p-2 justify-between'
        data-testid='new-expense-modal'
      >
        <div className='flex flex-col'>
          <MSpan className='font-bold text-xl mb-2'>
            Create A New Expense
          </MSpan>
          <MTextField
            id='expense-name-search' // Keep's 1Pass from hijacking normal name fields.
            name='name'
            label='What are you budgeting for?'
            required
            autoComplete='off'
            placeholder='Amazon, Netflix...'
          />
          <div className='flex gap-0 md:gap-4 flex-col md:flex-row'>
            <MAmountField
              name='amount'
              label='How much do you need?'
              required
              className='w-full md:w-1/2'
              allowNegative={ false }
            />
            <MDatePicker
              className='w-full md:w-1/2'
              label='When do you need it next?'
              min={ startOfTomorrow() }
              name='nextOccurrence'
              required
            />
          </div>
          <MSelectFunding
            menuPortalTarget={ document.body }
            label='When do you want to fund the expense?'
            required
            name='fundingScheduleId'
          />
          <MSelectFrequency
            dateFrom='nextOccurrence'
            menuPosition='fixed'
            menuShouldScrollIntoView={ false }
            menuShouldBlockScroll={ true }
            menuPortalTarget={ document.body }
            menuPlacement='bottom'
            label='How frequently do you need this expense?'
            placeholder='Select a spending frequency...'
            required
            name='ruleset'
          />
        </div>
        <div className='flex justify-end gap-2'>
          <MFormButton color='cancel' onClick={ modal.remove } data-testid='close-new-expense-modal'>
            Cancel
          </MFormButton>
          <MFormButton color='primary' type='submit'>
            Create
          </MFormButton>
        </div>
      </MForm>
    </MModal>
  );
}

const newExpenseModal = NiceModal.create(NewExpenseModal);

export default newExpenseModal;

export function showNewExpenseModal(): Promise<Spending | null> {
  return NiceModal.show<Spending | null, ExtractProps<typeof newExpenseModal>, {}>(newExpenseModal);
}
